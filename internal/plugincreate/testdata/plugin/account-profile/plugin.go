package accountprofile

import configuration "example.com/acme/my-app/v2/generated/configuration"

// Config is the generated configuration type for acme.my-app.account-profile.
type Config = configuration.AccountProfileConfig

// Plugin implements acme.my-app.account-profile.
type Plugin struct{}

// New constructs the acme.my-app.account-profile plugin.
func New(_ Config) *Plugin {
	return &Plugin{}
}
