package plugincreate

import (
	"errors"
	"testing"
)

func TestDeriveIDExcludesModuleHostAndMajorVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		modulePath string
		name       string
		want       string
	}{
		{modulePath: "example.com/acme/my-app", name: "account", want: "acme.my-app.account"},
		{modulePath: "example.com/acme/my-app/v2", name: "email-smtp", want: "acme.my-app.email-smtp"},
		{modulePath: "example.com/my-app", name: "account", want: "my-app.account"},
		{modulePath: "example.com/acme.co/my-app", name: "account", want: "acme.co.my-app.account"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.modulePath+"/"+test.name, func(t *testing.T) {
			t.Parallel()
			got, err := deriveID(test.modulePath, test.name)
			if err != nil || got != test.want {
				t.Fatalf("deriveID(%q, %q) = %q, %v; want %q", test.modulePath, test.name, got, err, test.want)
			}
		})
	}
}

func TestDeriveIDRejectsMissingOrNonCanonicalNamespaces(t *testing.T) {
	t.Parallel()

	for _, modulePath := range []string{"example.com", "example.com/Acme/app", "example.com/acme_org/app"} {
		modulePath := modulePath
		t.Run(modulePath, func(t *testing.T) {
			t.Parallel()
			if _, err := deriveID(modulePath, "account"); !errors.Is(err, ErrDeriveID) {
				t.Fatalf("deriveID(%q) error = %v, want ErrDeriveID", modulePath, err)
			}
		})
	}
}

func TestExportedDeriveIDAlsoValidatesPluginName(t *testing.T) {
	t.Parallel()

	if _, err := DeriveID("example.com/acme/app", "generated"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("DeriveID reserved name error = %v, want ErrInvalidName", err)
	}
	got, err := DeriveID("example.com/acme/app/v2", "account")
	if err != nil || got != "acme.app.account" {
		t.Fatalf("DeriveID = %q, %v", got, err)
	}
}

func TestGeneratedGoNamesAreDeterministicAndKeywordSafe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		packageName string
		title       string
	}{
		{name: "account", packageName: "account", title: "Account"},
		{name: "account-profile", packageName: "accountprofile", title: "Account Profile"},
		{name: "type", packageName: "typeplugin", title: "Type"},
	}
	for _, test := range tests {
		if got := goPackageName(test.name); got != test.packageName {
			t.Errorf("goPackageName(%q) = %q, want %q", test.name, got, test.packageName)
		}
		if got := title(test.name); got != test.title {
			t.Errorf("title(%q) = %q, want %q", test.name, got, test.title)
		}
	}
}
