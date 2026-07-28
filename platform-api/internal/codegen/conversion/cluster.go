package conversion

// ClusterServiceSetFields holds platform-injected values for cluster creation.
type ClusterServiceSetFields struct {
	CloudURL   string
	Placement  string
	CreatorARN string
}

// InjectClusterServiceSet merges service-set fields into a cluster spec map.
// Only non-empty values are injected. Placement is only set if not already
// present in the spec (allowing client-provided values to take precedence).
func InjectClusterServiceSet(spec map[string]interface{}, ssf ClusterServiceSetFields) {
	if ssf.CloudURL != "" {
		spec["cloudUrl"] = ssf.CloudURL
	}
	if ssf.Placement != "" {
		if spec["placement"] == nil || spec["placement"] == "" {
			spec["placement"] = ssf.Placement
		}
	}
	if ssf.CreatorARN != "" {
		spec["creatorARN"] = ssf.CreatorARN
	}
}

// RewriteCloudURLWithID sets cloudUrl to baseURL/clusterID in a response spec.
func RewriteCloudURLWithID(spec map[string]interface{}, baseURL, clusterID string) {
	spec["cloudUrl"] = baseURL + "/" + clusterID
}
