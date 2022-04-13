package rhoda

type ProviderAccount struct {
	providerName string
	secretName   string
	secretData   map[string][]byte
}
