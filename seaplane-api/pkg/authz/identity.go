package authz

// Validate applies the same structural checks used before policy resolution.
func (id Identity) Validate() error {
	_, err := checkedIdentity(id)
	return err
}
