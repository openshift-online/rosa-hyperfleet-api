package v1alpha1

// ClusterDNS mirrors upstream DNSSpec with custom markers.
// +hyperfleet:upstream-reduced-object=hypershiftv1beta1.DNSSpec
type ClusterDNS struct {
	// baseDomain is the base domain of the hosted cluster.
	// It will be used to configure ingress in the hosted cluster through the subdomain baseDomainPrefix.baseDomain.
	// If baseDomainPrefix is omitted, the hostedCluster.name will be used as the subdomain.
	// Once set, this field is immutable.
	// When the value is the empty string "", the controller might default to a value depending on the platform.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=service-set
	// +kubebuilder:validation:XValidation:rule=`self == "" || self.matches('^(?:(?:[a-zA-Z0-9-]+\\.)+[a-zA-Z]{2,}|[a-zA-Z0-9-]+)$')`,message="baseDomain must be a valid domain name (e.g., example, example.com, sub.example.com)"
	// +kubebuilder:validation:XValidation:rule=`oldSelf == "" || self == oldSelf`, message="baseDomain is immutable"
	// +kubebuilder:validation:MaxLength=253
	// +required
	BaseDomain string `json:"baseDomain"`

	// baseDomainPrefix is the base domain prefix for the hosted cluster ingress.
	// It will be used to configure ingress in the hosted cluster through the subdomain baseDomainPrefix.baseDomain.
	// If baseDomainPrefix is omitted, the hostedCluster.name will be used as the subdomain.
	// Set baseDomainPrefix to an empty string "", if you don't want a prefix at all (not even hostedCluster.name) to be prepended to baseDomain.
	// This field is immutable.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=service-set
	// +kubebuilder:validation:XValidation:rule=`self == "" || self.matches('^(?:(?:[a-zA-Z0-9-]+\\.)+[a-zA-Z]{2,}|[a-zA-Z0-9-]+)$')`,message="baseDomainPrefix must be a valid domain name (e.g., example, example.com, sub.example.com)"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="baseDomainPrefix is immutable"
	// +kubebuilder:validation:MaxLength=253
	// +optional
	BaseDomainPrefix *string `json:"baseDomainPrefix,omitempty"`

	// publicZoneID is the Hosted Zone ID where all the DNS records that are publicly accessible to the internet exist.
	// This field is optional and mainly leveraged in cloud environments where the DNS records for the .baseDomain are created by controllers in this zone.
	// Once set, this value is immutable.
	//
	// On Azure, this is a full Azure resource ID for a DNS Zone in the format:
	//   /subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/dnsZones/{zoneName}
	// The maximum length of 258 is derived from Azure resource naming limits.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=service-set
	// +kubebuilder:validation:XValidation:rule=`oldSelf == "" || self == oldSelf`, message="publicZoneID is immutable"
	// +kubebuilder:validation:MaxLength=258
	// +kubebuilder:validation:MinLength=1
	// +optional
	PublicZoneID string `json:"publicZoneID,omitempty"`

	// privateZoneID is the Hosted Zone ID where all the DNS records that are only available internally to the cluster exist.
	// This field is optional and mainly leveraged in cloud environments where the DNS records for the .baseDomain are created by controllers in this zone.
	// Once set, this value is immutable.
	//
	// On Azure, this is a full Azure resource ID for a Private DNS Zone in the format:
	//   /subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/privateDnsZones/{zoneName}
	// The maximum length of 265 is derived from Azure resource naming limits.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=service-set
	// +kubebuilder:validation:XValidation:rule=`oldSelf == "" || self == oldSelf`, message="privateZoneID is immutable"
	// +kubebuilder:validation:MaxLength=265
	// +kubebuilder:validation:MinLength=1
	// +optional
	PrivateZoneID string `json:"privateZoneID,omitempty"`
}
