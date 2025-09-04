package config

// ConfigResolver resolves configuration values from multiple sources with precedence
type ConfigResolver struct {
	sources []ConfigSource
}

func NewConfigResolver(sources ...ConfigSource) *ConfigResolver {
	return &ConfigResolver{sources: sources}
}

// ResolveString resolves string value from sources in order of precedence
func (r *ConfigResolver) ResolveString(key, defaultValue string) string {
	for _, source := range r.sources {
		if value, found := source.GetString(key); found {
			return value
		}
	}
	return defaultValue
}

// ResolveInt resolves int value from sources in order of precedence
func (r *ConfigResolver) ResolveInt(key string, defaultValue int) int {
	for _, source := range r.sources {
		if value, found := source.GetInt(key); found {
			return value
		}
	}
	return defaultValue
}

// ResolveFloat resolves float value from sources in order of precedence
func (r *ConfigResolver) ResolveFloat(key string, defaultValue float64) float64 {
	for _, source := range r.sources {
		if value, found := source.GetFloat(key); found {
			return value
		}
	}
	return defaultValue
}

// ResolveBool resolves bool value from sources in order of precedence
func (r *ConfigResolver) ResolveBool(key string, defaultValue bool) bool {
	for _, source := range r.sources {
		if value, found := source.GetBool(key); found {
			return value
		}
	}
	return defaultValue
}
