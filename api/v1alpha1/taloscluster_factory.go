package v1alpha1

// DefaultImageFactoryURL is the Image Factory of Sidero Labs, used when TalosClusterSpec.ImageFactoryURL is unset.
const DefaultImageFactoryURL = "https://factory.talos.dev"

// FactoryURL is the Image Factory base URL of the cluster: spec.imageFactoryURL, else DefaultImageFactoryURL.
func (s *TalosClusterSpec) FactoryURL() string {
	if s.ImageFactoryURL != "" {
		return s.ImageFactoryURL
	}

	return DefaultImageFactoryURL
}
