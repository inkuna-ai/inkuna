package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/inkuna-ai/inkuna/internal/config"
)

// RemoveSecretDeclaration forgets a secret entirely: the keychain value, the
// declaration in inkuna.yaml, and every runtime that exported it. Deleting
// only the value keeps the declaration; this is the way to stop declaring it.
func (service *Service) RemoveSecretDeclaration(name string) (ConfigurationView, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ConfigurationView{}, errors.New("a secret name is required")
	}
	// A server's secret is part of how it is declared, so the server is
	// changed first; checked before the value goes, which cannot be undone.
	for _, server := range service.config.File.MCPServers {
		for _, secret := range append(mapValues(server.Env), server.Bearer) {
			if secret == name {
				return ConfigurationView{}, fmt.Errorf("MCP server %q uses secret %s; change or remove the server first", server.Name, name)
			}
		}
	}
	if service.secretStore != nil {
		// Deleting a value that was never stored succeeds. Any other failure
		// stops here: the declaration must not go while the value remains.
		if err := service.secretStore.Delete(name); err != nil {
			return ConfigurationView{}, err
		}
	}
	if name == config.EnvLinearAPIKey {
		service.ForgetLinearAuthentication()
	}
	file := service.config.File
	file.Secrets = withoutSecret(file.Secrets, func(secret config.SecretRef) string { return secret.Name }, name)
	// The live configuration shares this map, so the edit goes into a copy.
	runtimes := make(config.RuntimesConfig, len(file.Runtimes))
	for runtimeName, entry := range file.Runtimes {
		entry.Secrets = withoutSecret(entry.Secrets, identity, name)
		runtimes[runtimeName] = entry
	}
	file.Runtimes = runtimes
	encoded, err := json.Marshal(file)
	if err != nil {
		return ConfigurationView{}, err
	}
	return service.SaveConfiguration(string(encoded))
}

func identity(value string) string { return value }

func mapValues(values map[string]string) []string {
	list := make([]string, 0, len(values))
	for _, value := range values {
		list = append(list, value)
	}
	return list
}

func withoutSecret[T any](items []T, name func(T) string, removed string) []T {
	kept := make([]T, 0, len(items))
	for _, item := range items {
		if name(item) != removed {
			kept = append(kept, item)
		}
	}
	return kept
}

// ChooseDirectory opens the native folder picker and returns the chosen
// absolute path, or "" when the user cancels. It only works inside the
// desktop app; the view falls back to typing the path.
func (service *Service) ChooseDirectory(title string) (string, error) {
	if service.ctx == nil || service.ctx.Value("frontend") == nil {
		return "", errors.New("the folder picker is available in the Inkuna desktop app")
	}
	return wailsruntime.OpenDirectoryDialog(service.ctx, wailsruntime.OpenDialogOptions{Title: title, CanCreateDirectories: true})
}
