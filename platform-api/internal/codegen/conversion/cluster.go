package conversion

// ClusterServiceSetFields holds platform-injected values for cluster creation.
type ClusterServiceSetFields struct {
	CloudURL   string
	Placement  string
	CreatorARN string
}

// InjectClusterServiceSet strips client-supplied service-set fields and
// replaces them with platform-injected values.
func InjectClusterServiceSet(spec map[string]interface{}, ssf ClusterServiceSetFields) {
	if spec == nil {
		return
	}
	delete(spec, "cloudUrl")
	if ssf.CloudURL != "" {
		spec["cloudUrl"] = ssf.CloudURL
	}

	delete(spec, "creatorARN")
	if ssf.CreatorARN != "" {
		spec["creatorARN"] = ssf.CreatorARN
	}

	if ssf.Placement != "" {
		existing, _ := spec["placement"].(string)
		if existing == "" {
			spec["placement"] = ssf.Placement
		}
	}
}

// RewriteCloudURLWithID sets cloudUrl to baseURL/clusterID in a response spec.
func RewriteCloudURLWithID(spec map[string]interface{}, baseURL, clusterID string) {
	if spec == nil {
		return
	}
	spec["cloudUrl"] = baseURL + "/" + clusterID
}
