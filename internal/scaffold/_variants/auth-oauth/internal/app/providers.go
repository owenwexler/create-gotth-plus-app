package app

import (
	"log/slog"
	// cgpa:if apple
	"os"
	// cgpa:end

	"github.com/owenwexler/gotth-plus/internal/auth"
	"github.com/owenwexler/gotth-plus/internal/config"
)

// newProviders builds the configured sign-in providers. Production refuses to start unless each one
// is configured (see config); in development, an unconfigured one is left out, with a warning.
func newProviders(env config.Env, endpoints map[string]auth.Endpoints) *auth.Providers {
	providers := &auth.Providers{}
	callback := func(name string) string { return env.BaseURL + "/auth/" + name + "/callback" }
	// cgpa:if google github apple
	urls := func(name string, standard auth.Endpoints) auth.Endpoints {
		if e, ok := endpoints[name]; ok {
			return e
		}
		return standard
	}
	// cgpa:end

	// cgpa:if google
	if env.GoogleClientID != "" && env.GoogleClientSecret != "" {
		providers.Add(auth.Google(env.GoogleClientID, env.GoogleClientSecret, callback("google"), urls("google", auth.GoogleEndpoints)))
	} else {
		slog.Warn("Google sign-in is off: set GoogleClientID and GoogleClientSecret", "redirect_uri", callback("google"))
	}
	// cgpa:end
	// cgpa:if github
	if env.GitHubClientID != "" && env.GitHubClientSecret != "" {
		providers.Add(auth.GitHub(env.GitHubClientID, env.GitHubClientSecret, callback("github"), urls("github", auth.GitHubEndpoints)))
	} else {
		slog.Warn("GitHub sign-in is off: set GitHubClientID and GitHubClientSecret", "callback_url", callback("github"))
	}
	// cgpa:end
	// cgpa:if apple
	if env.AppleClientID != "" && env.ApplePrivateKeyFile != "" {
		key, err := os.ReadFile(env.ApplePrivateKeyFile)
		if err != nil {
			panic("ApplePrivateKeyFile: " + err.Error())
		}
		apple, err := auth.Apple(auth.AppleConfig{ClientID: env.AppleClientID, TeamID: env.AppleTeamID, KeyID: env.AppleKeyID, PrivateKey: key},
			callback("apple"), urls("apple", auth.AppleEndpoints))
		if err != nil {
			panic(err.Error())
		}
		providers.Add(apple)
	} else {
		slog.Warn("Apple sign-in is off: set AppleClientID, AppleTeamID, AppleKeyID and ApplePrivateKeyFile", "return_url", callback("apple"))
	}
	// cgpa:end
	// cgpa:if oidc
	if env.OIDCIssuerURL != "" && env.OIDCClientID != "" && env.OIDCClientSecret != "" {
		providers.Add(auth.OIDC(env.OIDCLabel, env.OIDCIssuerURL, env.OIDCClientID, env.OIDCClientSecret, callback("oidc")))
	} else {
		slog.Warn("OIDC sign-in is off: set OIDCIssuerURL, OIDCClientID and OIDCClientSecret", "redirect_uri", callback("oidc"))
	}
	// cgpa:end
	return providers
}
