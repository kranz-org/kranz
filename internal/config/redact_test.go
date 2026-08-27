package config

import "testing"

func TestRedactEnvironmentValue(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		want     string
		redacted bool
	}{
		{name: "secret key", key: "API_TOKEN", value: "secret", want: "[redacted]", redacted: true},
		{name: "postgres password", key: "DATABASE_URL", value: "postgresql://app:secret@db.example/app", want: "[redacted]", redacted: true},
		{name: "redis password", key: "CACHE_URL", value: "redis://:secret@cache.example/0", want: "[redacted]", redacted: true},
		{name: "url without credentials", key: "PUBLIC_URL", value: "https://example.com/app", want: "https://example.com/app"},
		{name: "database url without password", key: "DATABASE_URL", value: "postgresql://db.example/app", want: "postgresql://db.example/app"},
		{name: "ordinary at sign", key: "CONTACT", value: "team@example.com", want: "team@example.com"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, redacted := redactEnvironmentValue(test.key, test.value)
			if got != test.want || redacted != test.redacted {
				t.Fatalf("redactEnvironmentValue(%q, %q) = (%q, %t), want (%q, %t)", test.key, test.value, got, redacted, test.want, test.redacted)
			}
		})
	}
}
