package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a/remote"
	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/secrets"
)

// RemoteAgentView is one remoteAgents entry as the Agents view shows it.
type RemoteAgentView struct {
	config.RemoteAgentConfig
	// SecretStatus says whether the agent's token has a value, never the value.
	SecretStatus *secrets.Status `json:"secretStatus,omitempty"`
	// Shadowed is set when a built-in or global agent has the same name, so the
	// remote agent is never used. A repository's agents can shadow it too; that
	// shows up as a note on the plan.
	Shadowed bool `json:"shadowed,omitempty"`
}

// CardChange is one difference between the card the user pinned and the card
// the agent serves now.
type CardChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// RemoteAgentPreview is a fetched card, ready for the user's review. Nothing
// is saved by producing one.
type RemoteAgentPreview struct {
	Card          remote.Card `json:"card"`
	Pin           remote.Pin  `json:"pin"`
	SuggestedName string      `json:"suggestedName"`
	// SamplePayload is what a typical stage would send. WithArtifacts is the
	// same stage when artifact bodies are allowed.
	SamplePayload              string `json:"samplePayload"`
	SamplePayloadWithArtifacts string `json:"samplePayloadWithArtifacts"`
	// Changed and Changes compare with the pin of the existing agent named in
	// the request, when there is one.
	Changed bool         `json:"changed"`
	Changes []CardChange `json:"changes"`
}

// RemoteAgentRequest is what the Connect and edit forms submit. Pinned data is
// never accepted from the view: Digest only names the card the user reviewed,
// and the card is fetched again before anything is stored.
type RemoteAgentRequest struct {
	Name             string `json:"name"`
	CardURL          string `json:"cardUrl"`
	Secret           string `json:"secret"`
	Auto             bool   `json:"auto"`
	SendArtifacts    bool   `json:"sendArtifacts"`
	MaxArtifactBytes int    `json:"maxArtifactBytes"`
	Enabled          bool   `json:"enabled"`
	Digest           string `json:"digest"`
}

// RemoteAgents lists every connected remote agent, enabled or not.
func (service *Service) RemoteAgents() ([]RemoteAgentView, error) {
	local, err := agent.Build("", service.config.AgentDirectories)
	if err != nil {
		return nil, err
	}
	views := make([]RemoteAgentView, 0, len(service.config.File.RemoteAgents))
	for _, entry := range service.config.File.RemoteAgents {
		view := RemoteAgentView{RemoteAgentConfig: entry}
		if entry.Secret != "" {
			for _, status := range service.secrets.Statuses([]string{entry.Secret}) {
				current := status
				view.SecretStatus = &current
			}
		}
		_, view.Shadowed = local.Get(entry.Name)
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views, nil
}

// PreviewRemoteAgent fetches and validates the card at cardURL. With the name
// of an existing agent it also reports what changed since that agent's card
// was pinned.
func (service *Service) PreviewRemoteAgent(cardURL, existingName string) (RemoteAgentPreview, error) {
	existing, known := service.config.File.RemoteAgent(strings.TrimSpace(existingName))
	if known && strings.TrimSpace(cardURL) == "" {
		cardURL = existing.CardURL
	}
	card, pin, err := service.fetchRemoteCard(cardURL, known && existing.AllowInsecure)
	if err != nil {
		return RemoteAgentPreview{}, err
	}
	preview := RemoteAgentPreview{Card: card, Pin: pin, SuggestedName: suggestAgentName(card.Name), Changes: []CardChange{}}
	if preview.SamplePayload, err = sampleAdvisorPayload(false); err != nil {
		return RemoteAgentPreview{}, err
	}
	if preview.SamplePayloadWithArtifacts, err = sampleAdvisorPayload(true); err != nil {
		return RemoteAgentPreview{}, err
	}
	if known && existing.Pinned != nil {
		preview.Changed = existing.Pinned.Digest != card.Digest
		preview.Changes = cardChanges(existing, card, pin)
	}
	return preview, nil
}

// SaveRemoteAgent connects a new agent or changes an existing one's settings.
// A new agent, a new card URL, or a request that names a digest pins the card
// served now, and only if it is the card the user reviewed.
func (service *Service) SaveRemoteAgent(requestJSON string) ([]RemoteAgentView, error) {
	var request RemoteAgentRequest
	decoder := json.NewDecoder(strings.NewReader(requestJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, fmt.Errorf("decode remote agent: %w", err)
	}
	request.Name, request.CardURL, request.Secret = strings.TrimSpace(request.Name), strings.TrimSpace(request.CardURL), strings.TrimSpace(request.Secret)
	existing, known := service.config.File.RemoteAgent(request.Name)
	entry := config.RemoteAgentConfig{
		Name: request.Name, CardURL: request.CardURL, Enabled: request.Enabled, Secret: request.Secret, Auto: request.Auto,
		SendArtifacts: request.SendArtifacts, MaxArtifactBytes: request.MaxArtifactBytes,
	}
	if known {
		// allowInsecure is only ever set by hand in inkuna.yaml.
		entry.AllowInsecure, entry.Description, entry.Skills, entry.Pinned = existing.AllowInsecure, existing.Description, existing.Skills, existing.Pinned
	}
	if !known || existing.CardURL != request.CardURL || request.Digest != "" || entry.Pinned == nil {
		if request.Digest == "" {
			return nil, errors.New("review the agent's card before saving it")
		}
		if err := service.pinRemoteCard(&entry, request.Digest); err != nil {
			return nil, err
		}
	}
	return service.saveRemoteAgents(func(entries []config.RemoteAgentConfig) []config.RemoteAgentConfig {
		for index := range entries {
			if entries[index].Name == entry.Name {
				entries[index] = entry
				return entries
			}
		}
		return append(entries, entry)
	})
}

// RepinRemoteAgent accepts the card an agent serves now, after the user has
// reviewed how it differs from the one they pinned.
func (service *Service) RepinRemoteAgent(name, reviewedDigest string) ([]RemoteAgentView, error) {
	entry, known := service.config.File.RemoteAgent(strings.TrimSpace(name))
	if !known {
		return nil, fmt.Errorf("remote agent %q is not connected", name)
	}
	if err := service.pinRemoteCard(&entry, reviewedDigest); err != nil {
		return nil, err
	}
	return service.saveRemoteAgents(func(entries []config.RemoteAgentConfig) []config.RemoteAgentConfig {
		for index := range entries {
			if entries[index].Name == entry.Name {
				entries[index] = entry
			}
		}
		return entries
	})
}

// SetRemoteAgentEnabled shows an agent to planning or hides it.
func (service *Service) SetRemoteAgentEnabled(name string, enabled bool) ([]RemoteAgentView, error) {
	if _, known := service.config.File.RemoteAgent(name); !known {
		return nil, fmt.Errorf("remote agent %q is not connected", name)
	}
	return service.saveRemoteAgents(func(entries []config.RemoteAgentConfig) []config.RemoteAgentConfig {
		for index := range entries {
			if entries[index].Name == name {
				entries[index].Enabled = enabled
			}
		}
		return entries
	})
}

// DeleteRemoteAgent disconnects an agent. Its secret stays declared: other
// things may use it, and removing a secret is its own decision.
func (service *Service) DeleteRemoteAgent(name string) ([]RemoteAgentView, error) {
	if _, known := service.config.File.RemoteAgent(name); !known {
		return nil, fmt.Errorf("remote agent %q is not connected", name)
	}
	return service.saveRemoteAgents(func(entries []config.RemoteAgentConfig) []config.RemoteAgentConfig {
		kept := entries[:0]
		for _, entry := range entries {
			if entry.Name != name {
				kept = append(kept, entry)
			}
		}
		return kept
	})
}

// fetchRemoteCard downloads a card under the production policy, or the
// development one for an agent already marked allowInsecure by hand.
func (service *Service) fetchRemoteCard(cardURL string, allowInsecure bool) (remote.Card, remote.Pin, error) {
	timeout := service.config.HealthTimeout
	if timeout < 10*time.Second {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(service.context(), timeout)
	defer cancel()
	card, err := remote.New(remote.Options{AllowInsecure: allowInsecure, RequestTimeout: timeout}).FetchCard(ctx, strings.TrimSpace(cardURL))
	if err != nil {
		return remote.Card{}, remote.Pin{}, err
	}
	pin, err := card.Pin(time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return remote.Card{}, remote.Pin{}, err
	}
	return card, pin, nil
}

// pinRemoteCard fetches the entry's card and pins it if it is the one the user
// reviewed. A card that changed between review and save is reviewed again.
func (service *Service) pinRemoteCard(entry *config.RemoteAgentConfig, reviewedDigest string) error {
	card, pin, err := service.fetchRemoteCard(entry.CardURL, entry.AllowInsecure)
	if err != nil {
		return err
	}
	if card.Digest != strings.TrimSpace(reviewedDigest) {
		return errors.New("the agent's card changed while you were reviewing it; review it again")
	}
	entry.Pinned = &pin
	entry.Description = truncateSummary(card.Description, 400)
	entry.Skills = make([]string, 0, len(card.Skills))
	for _, skill := range card.Skills {
		if name := truncateSummary(firstNonEmpty(skill.Name, skill.ID), 80); name != "" && len(entry.Skills) < 24 {
			entry.Skills = append(entry.Skills, name)
		}
	}
	return nil
}

// saveRemoteAgents applies an edit to a copy of the remoteAgents block and
// saves the file through the usual validation.
func (service *Service) saveRemoteAgents(edit func([]config.RemoteAgentConfig) []config.RemoteAgentConfig) ([]RemoteAgentView, error) {
	if _, err := service.editConfiguration(func(file *config.File) error {
		file.RemoteAgents = edit(append([]config.RemoteAgentConfig(nil), file.RemoteAgents...))
		if len(file.RemoteAgents) == 0 {
			file.RemoteAgents = nil
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return service.RemoteAgents()
}

// cardChanges compares the fields Inkuna keeps from a pinned card with the
// card served now. A card whose digest changed in none of these fields is
// reported as one opaque change, so the user is never told nothing changed.
func cardChanges(existing config.RemoteAgentConfig, card remote.Card, pin remote.Pin) []CardChange {
	changes := make([]CardChange, 0)
	add := func(field, before, after string) {
		if before != after {
			changes = append(changes, CardChange{Field: field, Before: before, After: after})
		}
	}
	flag := func(value bool) string {
		if value {
			return "yes"
		}
		return "no"
	}
	skills := make([]string, 0, len(card.Skills))
	for _, skill := range card.Skills {
		skills = append(skills, firstNonEmpty(skill.Name, skill.ID))
	}
	add("Endpoint", existing.Pinned.URL, pin.URL)
	add("Protocol version", existing.Pinned.ProtocolVersion, pin.ProtocolVersion)
	add("Authentication", firstNonEmpty(existing.Pinned.Auth, "none"), firstNonEmpty(pin.Auth, "none"))
	add("Streaming", flag(existing.Pinned.Streaming), flag(pin.Streaming))
	add("Description", existing.Description, truncateSummary(card.Description, 400))
	add("Skills", strings.Join(existing.Skills, ", "), strings.Join(skills, ", "))
	if len(changes) == 0 && existing.Pinned.Digest != card.Digest {
		changes = append(changes, CardChange{Field: "Other card content", Before: existing.Pinned.Digest, After: card.Digest})
	}
	return changes
}

// suggestAgentName turns a card's display name into an agent name.
func suggestAgentName(display string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(strings.TrimSpace(display)) {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			builder.WriteRune(character)
		case builder.Len() > 0 && !strings.HasSuffix(builder.String(), "-"):
			builder.WriteRune('-')
		}
	}
	name := strings.Trim(builder.String(), "-")
	if len(name) > 40 {
		name = strings.Trim(name[:40], "-")
	}
	if !agent.ValidName(name) {
		return "remote-advisor"
	}
	return name
}

// sampleAdvisorPayload renders the advisor handoff for a made-up stage, built
// by the same function as the real one, so the Connect flow shows the shape
// of what an agent will receive before anything is sent.
func sampleAdvisorPayload(withArtifacts bool) (string, error) {
	task := domain.TaskSpec{
		Version: domain.ContractVersion, ID: "task-sample", Kind: domain.TaskImplementation, Objective: "Add rate limiting to the public API",
		AcceptanceCriteria: []string{"Requests over the limit get HTTP 429", "Limits are configurable per route"},
		Constraints:        []string{"Keep the existing response format"},
		Source:             domain.TaskSource{Kind: "request"}, Repository: ".", Priority: 1, Budget: domain.Budget{WallTimeSeconds: 1800},
	}
	step := domain.WorkflowStep{ID: "security-advice", Title: "Security advice", Kind: domain.StepAgent, Permission: domain.PermissionReadOnly, Prompt: "Review the plan for abuse and denial-of-service risks."}
	diff := "--- a/api/middleware.go\n+++ b/api/middleware.go\n@@\n+func RateLimit(next http.Handler) http.Handler {\n"
	dependencies := []domain.HandoffResult{{
		Summary: "Planned a token-bucket limiter as middleware, keyed by API key.", Decisions: []string{"Limits live in configuration, not code"},
		Artifacts: []domain.ArtifactRef{{Name: "plan.diff", MediaType: "text/x-diff", Size: int64(len(diff)), Digest: "sha256:3f2a…"}},
	}}
	handoff, err := domain.BuildAdvisorHandoff(task, step, dependencies, []domain.HumanResponse{{Question: "Should limits apply to internal callers?", Answer: "No, only to public API keys."}})
	if err != nil {
		return "", err
	}
	if withArtifacts {
		handoff.ArtifactBodies = []domain.ArtifactBody{{Name: "plan.diff", Digest: "sha256:3f2a…", MediaType: "text/x-diff", Text: diff}}
	}
	encoded, err := json.MarshalIndent(handoff, "", "  ")
	return string(encoded), err
}
