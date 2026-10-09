package hyperfleetdb

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/model"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/resourceversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type storedMetadata struct {
	Labels          map[string]string       `json:"labels,omitempty"`
	Annotations     map[string]string       `json:"annotations,omitempty"`
	OwnerReferences []metav1.OwnerReference `json:"ownerReferences,omitempty"`
	Finalizers      []string                `json:"finalizers,omitempty"`
	Generation      int64                   `json:"generation,omitempty"`
}

func gvkToString(gvk schema.GroupVersionKind) string {
	return fmt.Sprintf("%s/%s/%s", gvk.Group, gvk.Version, gvk.Kind)
}

func stringToGVK(s string) (schema.GroupVersionKind, error) {
	parts := strings.SplitN(s, "/", 3)
	if len(parts) != 3 {
		return schema.GroupVersionKind{}, fmt.Errorf("invalid GVK string %q: expected group/version/kind", s)
	}
	return schema.GroupVersionKind{Group: parts[0], Version: parts[1], Kind: parts[2]}, nil
}

func resolveGVK(scheme *runtime.Scheme, obj runtime.Object) (schema.GroupVersionKind, error) {
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil {
		return schema.GroupVersionKind{}, fmt.Errorf("resolve GVK: %w", err)
	}
	if len(gvks) == 0 {
		return schema.GroupVersionKind{}, fmt.Errorf("no GVK registered for type %T", obj)
	}
	return gvks[0], nil
}

func itemGVKFromListGVK(gvk schema.GroupVersionKind) schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    strings.TrimSuffix(gvk.Kind, "List"),
	}
}

func extractSpec(obj client.Object) (json.RawMessage, error) {
	val := reflect.ValueOf(obj).Elem()
	specField := val.FieldByName("Spec")
	if !specField.IsValid() {
		return json.RawMessage(`{}`), nil
	}
	return json.Marshal(specField.Interface())
}

func hasFinalizers(r model.Resource) bool {
	if len(r.Metadata) == 0 {
		return false
	}
	var sm storedMetadata
	if err := json.Unmarshal(r.Metadata, &sm); err != nil {
		return false
	}
	return len(sm.Finalizers) > 0
}

func isFullyDeleted(r model.Resource) bool {
	return r.DeletionTimestamp != nil && !hasFinalizers(r)
}

func extractSpecStatus(obj client.Object) (spec, status json.RawMessage, err error) {
	val := reflect.ValueOf(obj).Elem()

	specField := val.FieldByName("Spec")
	if specField.IsValid() {
		spec, err = json.Marshal(specField.Interface())
		if err != nil {
			return nil, nil, fmt.Errorf("marshal spec: %w", err)
		}
	} else {
		spec = json.RawMessage(`{}`)
	}

	statusField := val.FieldByName("Status")
	if statusField.IsValid() {
		status, err = json.Marshal(statusField.Interface())
		if err != nil {
			return nil, nil, fmt.Errorf("marshal status: %w", err)
		}
	} else {
		status = json.RawMessage(`{}`)
	}

	return spec, status, nil
}

func extractStatus(obj client.Object) (json.RawMessage, error) {
	val := reflect.ValueOf(obj).Elem()
	statusField := val.FieldByName("Status")
	if !statusField.IsValid() {
		return json.RawMessage(`{}`), nil
	}
	return json.Marshal(statusField.Interface())
}

func extractMetadata(obj client.Object) (json.RawMessage, error) {
	sm := storedMetadata{
		Labels:          obj.GetLabels(),
		Annotations:     obj.GetAnnotations(),
		OwnerReferences: obj.GetOwnerReferences(),
		Finalizers:      obj.GetFinalizers(),
		Generation:      obj.GetGeneration(),
	}
	return json.Marshal(sm)
}

// ValidateObjectResourceVersion requires a usable positive CAS version without
// changing the optional-version behavior of general controller-runtime writes.
func ValidateObjectResourceVersion(obj client.Object) error {
	if obj == nil || (reflect.ValueOf(obj).Kind() == reflect.Pointer && reflect.ValueOf(obj).IsNil()) {
		return fmt.Errorf("object is required")
	}
	version, err := parseResourceVersion(obj)
	if err != nil {
		return err
	}
	if version <= 0 {
		return fmt.Errorf("a positive object resource version is required")
	}
	return nil
}

func parseResourceVersion(obj client.Object) (int64, error) {
	rv := obj.GetResourceVersion()
	if rv == "" {
		return 0, nil
	}
	if v, err := strconv.ParseInt(rv, 10, 64); err == nil {
		return v, nil
	}
	// Objects delivered by watch events carry a composite RV with the object
	// version as an "o<n>;" prefix (see pgWatcher.relay).
	composite, err := resourceversion.Parse(rv)
	if err != nil || composite.ObjectVersion == 0 {
		return 0, fmt.Errorf("resource version %q carries no object version", rv)
	}
	return composite.ObjectVersion, nil
}

func resourceToObject(r model.Resource, scheme *runtime.Scheme) (client.Object, error) {
	gvk, err := stringToGVK(r.GVK)
	if err != nil {
		return nil, err
	}

	runtimeObj, err := scheme.New(gvk)
	if err != nil {
		return nil, fmt.Errorf("scheme.New(%v): %w", gvk, err)
	}
	obj, ok := runtimeObj.(client.Object)
	if !ok {
		return nil, fmt.Errorf("type %T does not implement client.Object", runtimeObj)
	}

	populateObjectMeta(obj, r)
	obj.GetObjectKind().SetGroupVersionKind(gvk)

	if err := injectSpecStatus(obj, r.Spec, r.Status); err != nil {
		return nil, err
	}

	return obj, nil
}

func populateObject(dst client.Object, r model.Resource, gvk schema.GroupVersionKind) error {
	populateObjectMeta(dst, r)
	dst.GetObjectKind().SetGroupVersionKind(gvk)
	return injectSpecStatus(dst, r.Spec, r.Status)
}

func populateObjectMeta(obj client.Object, r model.Resource) {
	obj.SetName(r.Name)
	obj.SetNamespace(r.Namespace)
	obj.SetUID(types.UID(r.UID.String()))
	obj.SetResourceVersion(strconv.FormatInt(r.ObjectVersion, 10))
	obj.SetCreationTimestamp(metav1.NewTime(r.CreatedAt))
	if r.DeletionTimestamp != nil {
		dt := metav1.NewTime(*r.DeletionTimestamp)
		obj.SetDeletionTimestamp(&dt)
	}

	if len(r.Metadata) > 0 {
		var sm storedMetadata
		if err := json.Unmarshal(r.Metadata, &sm); err == nil {
			obj.SetLabels(sm.Labels)
			obj.SetAnnotations(sm.Annotations)
			obj.SetOwnerReferences(sm.OwnerReferences)
			obj.SetFinalizers(sm.Finalizers)
			obj.SetGeneration(sm.Generation)
		}
	}
}

func injectSpecStatus(obj client.Object, spec, status json.RawMessage) error {
	val := reflect.ValueOf(obj).Elem()

	if len(spec) > 0 && string(spec) != "{}" {
		specField := val.FieldByName("Spec")
		if specField.IsValid() && specField.CanAddr() {
			if err := json.Unmarshal(spec, specField.Addr().Interface()); err != nil {
				return fmt.Errorf("unmarshal spec: %w", err)
			}
		}
	}

	if len(status) > 0 && string(status) != "{}" {
		statusField := val.FieldByName("Status")
		if statusField.IsValid() && statusField.CanAddr() {
			if err := json.Unmarshal(status, statusField.Addr().Interface()); err != nil {
				return fmt.Errorf("unmarshal status: %w", err)
			}
		}
	}

	return nil
}

func setListItems(list client.ObjectList, items []client.Object) error {
	val := reflect.ValueOf(list).Elem()
	itemsField := val.FieldByName("Items")
	if !itemsField.IsValid() {
		return fmt.Errorf("type %T has no Items field", list)
	}
	if itemsField.Kind() != reflect.Slice {
		return fmt.Errorf("type %T Items field is not a slice", list)
	}

	elemType := itemsField.Type().Elem()
	slice := reflect.MakeSlice(itemsField.Type(), 0, len(items))

	for _, item := range items {
		itemVal := reflect.ValueOf(item)
		if itemVal.Kind() == reflect.Pointer {
			if itemVal.Type().Elem() == elemType {
				slice = reflect.Append(slice, itemVal.Elem())
			} else {
				return fmt.Errorf("item type %T does not match slice element type %v", item, elemType)
			}
		} else if itemVal.Type() == elemType {
			slice = reflect.Append(slice, itemVal)
		} else {
			return fmt.Errorf("item type %T does not match slice element type %v", item, elemType)
		}
	}

	itemsField.Set(slice)
	return nil
}
