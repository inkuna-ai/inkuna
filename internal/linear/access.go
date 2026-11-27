package linear

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
)

// Access is what a key may do of one sort of write.
type Access string

const (
	AccessAllowed Access = "allowed"
	AccessDenied  Access = "denied"
	// AccessUnknown is an answer that did not say either way.
	AccessUnknown Access = "unknown"
)

// WriteAccess is what Linear said a key may write. A key can be limited to
// reading, to creating issues, or to commenting, and to some teams; the
// last is only found out by a write to another team's issue.
type WriteAccess struct {
	// Update covers changing an issue: its status, its fields, and links.
	Update  Access `json:"update"`
	Comment Access `json:"comment"`
	Create  Access `json:"create"`
	// Reasons holds what Linear answered for each denied or unknown sort.
	Reasons map[string]string `json:"reasons,omitempty"`
}

// WriteAccess asks Linear which writes the key may make without changing
// anything: each probe is a mutation of an issue or team that does not
// exist. Linear checks a key's permissions before it looks the entity up,
// so a key that may not make the write is refused for its scope, and one
// that may is told the entity was not found. Each probe names a fresh random
// id: Linear lets a client choose a new entity's id, so a fixed one could be
// made to exist.
func (client *Client) WriteAccess(ctx context.Context) (WriteAccess, error) {
	access := WriteAccess{Reasons: map[string]string{}}
	probes := []struct {
		name     string
		target   *Access
		mutation string
	}{
		{"update", &access.Update, `mutation InkunaCanUpdate { issueUpdate(id: "%s", input: {}) { success } }`},
		{"comment", &access.Comment, `mutation InkunaCanComment { commentCreate(input: { issueId: "%s", body: "Inkuna write check" }) { success } }`},
		{"create", &access.Create, `mutation InkunaCanCreate { issueCreate(input: { teamId: "%s", title: "Inkuna write check" }) { success } }`},
	}
	for _, probe := range probes {
		absent, err := randomUUID()
		if err != nil {
			return WriteAccess{}, fmt.Errorf("check Linear write access: %w", err)
		}
		var response struct {
			Errors []graphqlError `json:"errors"`
		}
		answer := client.query(ctx, fmt.Sprintf(probe.mutation, absent), nil, &response)
		var failure *apiError
		switch {
		case errors.As(answer, &failure):
			response.Errors = failure.errors
		case answer != nil:
			return WriteAccess{}, fmt.Errorf("check Linear write access: %w", answer)
		}
		*probe.target = classifyProbe(response.Errors)
		if *probe.target != AccessAllowed && len(response.Errors) > 0 {
			access.Reasons[probe.name] = graphQLErrors(response.Errors).Error()
		}
	}
	return access, nil
}

// classifyProbe reads a probe's answer. Only a refusal for the key's scope
// or permission means no, and only an entity Linear could not find means
// yes; anything else is unknown rather than a guess.
func classifyProbe(errors []graphqlError) Access {
	if len(errors) == 0 {
		// The mutation cannot succeed on an absent id; an answer without an
		// error says nothing about the key.
		return AccessUnknown
	}
	for _, value := range errors {
		message := strings.ToLower(value.Message)
		if strings.Contains(message, "scope") || strings.Contains(message, "permission") || strings.Contains(message, "not authorized") ||
			strings.EqualFold(value.Extensions.Code, "FORBIDDEN") || strings.EqualFold(value.Extensions.Type, "forbidden") {
			return AccessDenied
		}
	}
	if notFound(errors) {
		return AccessAllowed
	}
	return AccessUnknown
}

// notFound reports whether Linear answered that an entity does not exist,
// which is also its answer for one the key cannot see.
func notFound(errors []graphqlError) bool {
	for _, value := range errors {
		if strings.Contains(strings.ToLower(value.Message), "not found") || strings.EqualFold(value.Extensions.Code, "ENTITY_NOT_FOUND") {
			return true
		}
	}
	return false
}

// randomUUID returns a random version 4 UUID.
func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
