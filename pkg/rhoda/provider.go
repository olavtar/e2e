package rhoda

type ProviderAccount struct {
	ProviderName string
	SecretName   string
	SecretData   map[string][]byte
}
