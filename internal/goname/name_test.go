package goname_test

import (
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/goname"
)

func TestCapabilityNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		identifier  string
		packageName string
		operation   string
	}{
		{identifier: "email.send/v1", packageName: "emailsendv1", operation: "Send"},
		{identifier: "workspace.invite-member/v2", packageName: "workspaceinvitememberv2", operation: "InviteMember"},
		{identifier: "account.lookup-by-id/v12", packageName: "accountlookupbyidv12", operation: "LookupByID"},
		{identifier: "gateway.send-http/v18446744073709551615", packageName: "gatewaysendhttpv18446744073709551615", operation: "SendHTTP"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.identifier, func(t *testing.T) {
			t.Parallel()
			identifier, err := capabilityid.Parse(test.identifier)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := goname.Package(identifier); got != test.packageName {
				t.Fatalf("Package = %q, want %q", got, test.packageName)
			}
			if got := goname.Operation(identifier); got != test.operation {
				t.Fatalf("Operation = %q, want %q", got, test.operation)
			}
		})
	}
}
