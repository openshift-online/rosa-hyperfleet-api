package hyperfleetdb_test

import (
	"testing"

	fleetdb "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateObjectResourceVersion(t *testing.T) {
	for _, tc := range []struct {
		rv    string
		valid bool
	}{{"", false}, {"0", false}, {"-1", false}, {"bad", false}, {"o0;12", false}, {"o-1;12", false}, {"o1;", false}, {"o1;bad", false}, {"1", true}, {"9223372036854775807", true}, {"o3;12345678", true}} {
		t.Run(tc.rv, func(t *testing.T) {
			obj := &Widget{ObjectMeta: metav1.ObjectMeta{ResourceVersion: tc.rv}}
			if err := fleetdb.ValidateObjectResourceVersion(obj); (err == nil) != tc.valid {
				t.Fatalf("rv=%q valid=%v err=%v", tc.rv, tc.valid, err)
			}
		})
	}
	if err := fleetdb.ValidateObjectResourceVersion(nil); err == nil {
		t.Fatal("nil object accepted")
	}
	t.Run("typed nil", func(t *testing.T) {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Errorf("typed nil validator panicked: %v", recovered)
			}
		}()
		var obj *Widget
		if err := fleetdb.ValidateObjectResourceVersion(obj); err == nil {
			t.Fatal("typed nil object accepted")
		}
	})
}
