package app

import (
	"log/slog"
	"regexp"
	"strings"

	"github.com/owenwexler/gotth-plus/internal/view"
)

// supabaseLabels are the button labels of Supabase's providers; others use their name.
var supabaseLabels = map[string]string{
	"apple": "Apple", "azure": "Microsoft", "bitbucket": "Bitbucket", "discord": "Discord", "facebook": "Facebook",
	"figma": "Figma", "github": "GitHub", "gitlab": "GitLab", "google": "Google", "kakao": "Kakao", "keycloak": "Keycloak",
	"linkedin_oidc": "LinkedIn", "notion": "Notion", "slack_oidc": "Slack", "spotify": "Spotify", "twitch": "Twitch",
	"twitter": "X", "workos": "WorkOS", "zoom": "Zoom",
}

var providerName = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// supabaseProviderButtons reads SupabaseProviders ("google,github"): the providers enabled in the
// Supabase dashboard that get a "Continue with ..." button.
func supabaseProviderButtons(list string) (map[string]string, []view.LoginProvider) {
	providers := map[string]string{}
	var buttons []view.LoginProvider
	for _, name := range strings.Split(list, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if !providerName.MatchString(name) || providers[name] != "" {
			slog.Warn("SupabaseProviders: skipping an invalid or repeated name", "name", name)
			continue
		}
		label := supabaseLabels[name]
		if label == "" {
			label = name
		}
		providers[name] = name
		buttons = append(buttons, view.LoginProvider{Name: name, Label: label})
	}
	return providers, buttons
}
