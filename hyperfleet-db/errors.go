package hyperfleetdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/model"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/writer"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ErrInvalidContinueToken is returned when a continue token is malformed or
// does not match the expected account context.
var ErrInvalidContinueToken = errors.New("pgruntime: invalid continue token")

var (
	errDryRunNotSupported            = fmt.Errorf("pgruntime: DryRun not supported")
	errPropagationPolicyNotSupported = fmt.Errorf(
		"pgruntime: PropagationPolicy not supported — use finalizers for cleanup")
	errPreconditionsNotSupported = fmt.Errorf(
		"pgruntime: Preconditions not supported — use ResourceVersion on the object")
	errGracePeriodNotSupported = fmt.Errorf(
		"pgruntime: GracePeriodSeconds not supported" +
			" — handle graceful shutdown in your reconciler")
	errGenerateNameNotSupported = fmt.Errorf(
		"pgruntime: GenerateName not supported" +
			" — set Name explicitly before Create()")
)

func rejectDryRun(dryRun []string) error {
	if len(dryRun) > 0 {
		return errDryRunNotSupported
	}
	return nil
}

func rejectUnsupportedDeleteOpts(opts client.DeleteOptions) error {
	if err := rejectDryRun(opts.DryRun); err != nil {
		return err
	}
	if opts.PropagationPolicy != nil {
		return errPropagationPolicyNotSupported
	}
	if opts.Preconditions != nil {
		return errPreconditionsNotSupported
	}
	if opts.GracePeriodSeconds != nil {
		return errGracePeriodNotSupported
	}
	return nil
}

func groupResource(gvk schema.GroupVersionKind) schema.GroupResource {
	return schema.GroupResource{
		Group:    gvk.Group,
		Resource: strings.ToLower(gvk.Kind),
	}
}

func mapGetError(err error, gvk schema.GroupVersionKind, name string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apierrors.NewNotFound(groupResource(gvk), name)
	}
	return err
}

func mapWriteError(
	ctx context.Context, w *writer.Writer, err error,
	gvk schema.GroupVersionKind, name string, _ int64,
) (*model.Resource, error) {
	if errors.Is(err, writer.ErrAlreadyExists) {
		return nil, apierrors.NewAlreadyExists(groupResource(gvk), name)
	}
	if errors.Is(err, writer.ErrConflict) {
		return nil, apierrors.NewConflict(groupResource(gvk), name, err)
	}

	var ace *writer.AmbiguousCommitError
	if errors.As(err, &ace) {
		r, rbErr := w.ReadBack(ctx, ace.GVK, ace.Namespace, ace.Name, ace.Txid)
		if rbErr != nil {
			return nil, fmt.Errorf("ambiguous commit + read-back failed: %w (original: %v)", rbErr, err)
		}
		if r == nil {
			return nil, apierrors.NewConflict(groupResource(gvk), name, err)
		}
		return r, nil
	}

	return nil, err
}
