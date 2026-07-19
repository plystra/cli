package command

import (
	"reflect"
	"testing"
)

func TestParseUseArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		want      useArguments
	}{
		{
			name:      "default",
			arguments: []string{"use", "email.send/v1", "acme.email.smtp"},
			want:      useArguments{capability: "email.send/v1", pluginID: "acme.email.smtp"},
		},
		{
			name:      "environment",
			arguments: []string{"use", "email.send/v1", "acme.email.smtp", "--env", "production"},
			want:      useArguments{capability: "email.send/v1", pluginID: "acme.email.smtp", environment: "production"},
		},
		{
			name:      "configuration",
			arguments: []string{"use", "email.send/v1", "acme.email.smtp", "--config", "deploy/customer.yaml"},
			want:      useArguments{capability: "email.send/v1", pluginID: "acme.email.smtp", config: "deploy/customer.yaml"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseUseArguments(test.arguments)
			if !ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseUseArguments(%q) = %#v, %t; want %#v", test.arguments, got, ok, test.want)
			}
		})
	}
}

func TestParseUseArgumentsRejectsInvalidForms(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{
		nil,
		{"use"},
		{"use", "email.send/v1"},
		{"use", "--env", "production"},
		{"use", "email.send/v1", "--env", "production"},
		{"use", "email.send/v1", "acme.email", "extra"},
		{"use", "email.send/v1", "acme.email", "--env"},
		{"use", "email.send/v1", "acme.email", "--config"},
		{"use", "email.send/v1", "acme.email", "--env", "test", "--env", "production"},
		{"use", "email.send/v1", "acme.email", "--config", "a.yaml", "--config", "b.yaml"},
		{"use", "email.send/v1", "acme.email", "--env", "test", "--config", "deploy.yaml"},
	} {
		if got, ok := parseUseArguments(arguments); ok {
			t.Fatalf("parseUseArguments(%q) = %#v, true; want false", arguments, got)
		}
	}
}
