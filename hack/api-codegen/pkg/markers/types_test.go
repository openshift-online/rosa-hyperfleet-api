package markers

import "testing"

func TestHiddenFromPublicAPI(t *testing.T) {
	tests := []struct {
		name string
		meta FieldMeta
		want bool
	}{
		{name: "visible", meta: FieldMeta{WriteMode: Mutable}, want: false},
		{name: "hidden service-set", meta: FieldMeta{Hidden: true, WriteMode: ServiceSet}, want: true},
		{name: "hidden mutable remains public", meta: FieldMeta{Hidden: true, WriteMode: Mutable}, want: false},
		{name: "reduced container remains public", meta: FieldMeta{Hidden: true, WriteMode: ServiceSet, IsReducedContainer: true}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.meta.HiddenFromPublicAPI(); got != tt.want {
				t.Fatalf("HiddenFromPublicAPI() = %t, want %t", got, tt.want)
			}
		})
	}
}
