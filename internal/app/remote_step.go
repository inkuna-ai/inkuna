package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/a2a/remote"
	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
)

const (
	// RemoteRequestArtifact names the artifact holding exactly what was sent to
	// a remote agent. The thread shows it as the outbound payload.
	RemoteRequestArtifact = "remote-request"
	remoteAdviceLimit     = 1500
	remoteStatusLimit     = 400
	remoteCancelTimeout   = 5 * time.Second
)

// advisorParts builds what a remote advisor is sent for a step. The same
// parts travel on the bus and, unchanged, over the network, so the thread
// shows what left the machine. See docs/remote-a2a.md.
func (service *Service) advisorParts(ctx context.Context, run *liveExecution, step domain.WorkflowStep, entry config.RemoteAgentConfig, dependencies []domain.HandoffResult) ([]a2a.Part, error) {
	answered, err := service.store.AnsweredInputs(ctx, run.view.ID)
	if err != nil {
		return nil, fmt.Errorf("load answered questions: %w", err)
	}
	responses := make([]domain.HumanResponse, 0, len(answered))
	for _, input := range answered {
		responses = append(responses, domain.HumanResponse{Question: input.Question, Answer: input.Answer})
	}
	handoff, err := domain.BuildAdvisorHandoff(run.task, step, dependencies, responses)
	if err != nil {
		return nil, err
	}
	if entry.SendArtifacts {
		handoff.ArtifactBodies = service.artifactBodies(handoff.Artifacts, entry.ArtifactBytes())
	}
	data, err := jsonObject(handoff)
	if err != nil {
		return nil, err
	}
	return []a2a.Part{a2a.TextPart(run.task.Objective), a2a.DataPart(data)}, nil
}

// artifactBodies inlines upstream text artifacts until the byte cap is spent.
// Anything that is not text is left as the reference it already is.
func (service *Service) artifactBodies(refs []domain.ArtifactRef, limit int) []domain.ArtifactBody {
	bodies := make([]domain.ArtifactBody, 0)
	seen := make(map[string]bool)
	for _, ref := range refs {
		if limit <= 0 {
			break
		}
		if seen[ref.Digest] || !textMediaType(ref.MediaType) {
			continue
		}
		seen[ref.Digest] = true
		text, truncated, err := service.readArtifact(ref.Digest, int64(limit))
		if err != nil || strings.ContainsRune(text, 0) {
			continue
		}
		// A cut can land inside a multi-byte character.
		text = strings.ToValidUTF8(text, "")
		limit -= len(text)
		bodies = append(bodies, domain.ArtifactBody{Name: ref.Name, Digest: ref.Digest, MediaType: ref.MediaType, Text: text, Truncated: truncated})
	}
	return bodies
}

func textMediaType(mediaType string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(strings.SplitN(mediaType, ";", 2)[0]))
	return strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || mediaType == "application/yaml" || mediaType == "application/xml"
}

func jsonObject(value any) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	return data, nil
}

// serveRemoteTask is the bus handler side of a remote advisor: it checks the
// pinned card, sends the message it was given, follows the remote task, and
// turns the answer into a handoff. Nothing the agent says is acted on here;
// its answer is stored and summarized as untrusted advice.
func (service *Service) serveRemoteTask(ctx context.Context, bus *a2a.Bus, task a2a.Task, message a2a.Message, assignment *workerAssignment) error {
	entry := *assignment.remote
	name := assignment.definition.Name
	if len(task.History) > 1 {
		return fmt.Errorf("remote agent %s takes one message per stage and cannot be sent follow-ups", name)
	}
	if entry.Pinned == nil {
		return fmt.Errorf("remote agent %s has no reviewed card", name)
	}
	wallTime := assignment.step.Budget.WallTimeSeconds
	if wallTime <= 0 {
		wallTime = 1800
	}
	ctx, stop := context.WithTimeout(ctx, time.Duration(wallTime)*time.Second)
	defer stop()
	// The local task ending (a cancelled run) must end the network exchange.
	if events, unsubscribe, err := bus.Subscribe(task.ID); err == nil {
		defer unsubscribe()
		go func() {
			for {
				select {
				case _, open := <-events:
					if !open {
						stop()
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	note := func(kind domain.EventKind, summary string) {
		_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, kind, summary))
	}

	client := remote.New(remote.Options{AllowInsecure: entry.AllowInsecure})
	if _, err := client.Verify(ctx, entry.CardURL, *entry.Pinned); err != nil {
		if errors.Is(err, remote.ErrCardChanged) {
			return fmt.Errorf("the Agent Card of %s changed since you reviewed it; nothing was sent. Review and re-pin it in Agents before it is used again", name)
		}
		return fmt.Errorf("check the Agent Card of %s: %w", name, err)
	}
	token := ""
	if entry.Secret != "" {
		value, _, err := service.dependenciesFor(assignment.executionID).secrets.Resolve(entry.Secret)
		if err != nil {
			return fmt.Errorf("resolve secret %s for %s: %w", entry.Secret, name, err)
		}
		if value == "" {
			return fmt.Errorf("remote agent %s needs secret %s, which has no value in the keychain or environment", name, entry.Secret)
		}
		token = value
	}
	// Whatever comes back is shown to the user and summarized for other
	// agents; an agent that echoes its credential must not get it stored.
	scrub := func(value string) string {
		if token == "" {
			return value
		}
		return strings.ReplaceAll(value, token, "[REDACTED]")
	}
	session, err := client.Session(*entry.Pinned, token)
	if err != nil {
		return err
	}

	params := a2a.MessageSendParams{Message: a2a.Message{MessageID: a2a.NewID("msg"), Role: a2a.RoleUser, Parts: message.Parts, Timestamp: time.Now().UTC()}}
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	// Record first: what cannot be shown to the user is not sent.
	ref, err := service.storeArtifact(ctx, assignment.task.ID, assignment.executionID, assignment.step.ID, name, RemoteRequestArtifact, assignment.step.ID+"-remote-request.json", "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("record the outbound payload: %w", err)
	}
	endpoint := entry.Pinned.URL
	if parsed, err := url.Parse(endpoint); err == nil {
		endpoint = parsed.Host
	}
	if err := bus.AddArtifact(ctx, task.ID, a2a.Artifact{
		Name: RemoteRequestArtifact, Description: "Exactly what was sent to " + name + " at " + endpoint,
		Parts: []a2a.Part{a2a.TextPart(string(payload)), a2a.FilePart(ref.Name, ref.MediaType, "inkuna://artifact/"+ref.Digest)},
		// outbound is how the thread tells this artifact from the agent's own:
		// a remote artifact's metadata is never copied, so only Inkuna sets it.
		Metadata: map[string]any{"outbound": true, "endpoint": entry.Pinned.URL, "bytes": len(payload)},
	}); err != nil {
		return err
	}
	service.runsMu.Lock()
	if run := service.runs[assignment.executionID]; run != nil {
		run.outbound[assignment.step.ID] = string(payload)
	}
	service.runsMu.Unlock()
	note(domain.EventLifecycle, fmt.Sprintf("Sent %d bytes to remote agent %s at %s", len(payload), name, endpoint))

	lastStatus := ""
	result, err := session.Run(ctx, params, func(update remote.Update) error {
		status := update.Status
		if update.Task != nil {
			status = &update.Task.Status
		}
		if status == nil || update.Final || status.Message == nil {
			return nil
		}
		text := truncateSummary(scrub(status.Message.Text()), remoteStatusLimit)
		if text == "" || text == lastStatus {
			return nil
		}
		lastStatus = text
		note(domain.EventMessage, name+": "+text)
		// A finished local task refuses the update, which ends the exchange.
		return bus.UpdateStatus(ctx, task.ID, a2a.StateWorking, &a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.TextPart(text)}})
	})
	abandon := func() {
		cancelCtx, cancel := context.WithTimeout(context.Background(), remoteCancelTimeout)
		defer cancel()
		_ = session.Cancel(cancelCtx, result.ID)
	}
	if err != nil {
		abandon()
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("remote agent %s did not finish within %d seconds", name, wallTime)
		}
		return errors.New(scrub(err.Error()))
	}
	reply := ""
	if result.Status.Message != nil {
		reply = scrub(result.Status.Message.Text())
	}
	switch state := result.Status.State; {
	case state == a2a.StateCompleted:
	case state.Interrupted():
		abandon()
		return fmt.Errorf("remote agent %s asked for input (%s); advisors cannot ask questions through Inkuna", name, state)
	default:
		return fmt.Errorf("remote agent %s ended the task as %s: %s", name, state, firstNonEmpty(truncateSummary(reply, remoteStatusLimit), "no reason given"))
	}

	// The handoff is built here from text and stored artifacts only. A remote
	// agent has no way to raise questions, message peers, claim decisions, or
	// add tasks, because nothing it sends is decoded into those fields.
	handoff := domain.HandoffResult{Version: domain.ContractVersion, Status: domain.HandoffCompleted}
	for index, artifact := range result.Artifacts {
		local, refs, text := service.keepRemoteArtifact(ctx, assignment, artifact, index, scrub)
		handoff.Artifacts = append(handoff.Artifacts, refs...)
		if reply == "" {
			reply = text
		}
		_ = bus.AddArtifact(ctx, task.ID, local)
	}
	if reply != "" {
		if ref, err := service.storeArtifact(ctx, assignment.task.ID, assignment.executionID, assignment.step.ID, name, "remote-reply", assignment.step.ID+"-advice.txt", "text/plain", strings.NewReader(reply)); err == nil {
			handoff.Artifacts = append(handoff.Artifacts, ref)
		}
	}
	handoff.Summary = fmt.Sprintf("Advice from remote agent %s (untrusted): %s", name, firstNonEmpty(truncateSummary(reply, remoteAdviceLimit), "the agent returned no text"))
	// Stored like a local worker's handoff, so the step's context can be
	// restored after the run has left memory.
	if data, err := json.MarshalIndent(handoff, "", "  "); err == nil {
		_, _ = service.storeArtifact(ctx, assignment.task.ID, assignment.executionID, assignment.step.ID, name, "handoff", assignment.step.ID+"-handoff.json", "application/json", bytes.NewReader(data))
	}
	assignment.mu.Lock()
	assignment.handoff = &handoff
	assignment.mu.Unlock()
	note(domain.EventComplete, truncateSummary(handoff.Summary, 280))
	return bus.UpdateStatus(ctx, task.ID, a2a.StateCompleted, &a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.TextPart(firstNonEmpty(reply, "The agent returned no text."))}})
}

// keepRemoteArtifact stores what a remote artifact carries and returns the
// copy shown in the thread: text and data as they are, inline files moved into
// the artifact store, and files by URL reduced to their name, because Inkuna
// never fetches an address a remote agent hands it.
func (service *Service) keepRemoteArtifact(ctx context.Context, assignment *workerAssignment, artifact a2a.Artifact, index int, scrub func(string) string) (a2a.Artifact, []domain.ArtifactRef, string) {
	name := scrub(firstNonEmpty(artifact.Name, fmt.Sprintf("advice-%d", index+1)))
	local := a2a.Artifact{Name: truncateSummary(name, 120), Description: truncateSummary(scrub(artifact.Description), 280), Metadata: map[string]any{"remote": true}}
	refs := make([]domain.ArtifactRef, 0)
	store := func(suffix, mediaType string, data []byte) (domain.ArtifactRef, bool) {
		ref, err := service.storeArtifact(ctx, assignment.task.ID, assignment.executionID, assignment.step.ID, assignment.definition.Name, "remote-reply", safeArtifactName(name)+suffix, mediaType, bytes.NewReader(data))
		if err != nil {
			return domain.ArtifactRef{}, false
		}
		refs = append(refs, ref)
		return ref, true
	}
	var text strings.Builder
	for _, part := range artifact.Parts {
		switch part.Kind {
		case a2a.PartText:
			text.WriteString(scrub(part.Text))
			local.Parts = append(local.Parts, a2a.TextPart(scrub(part.Text)))
		case a2a.PartData:
			if raw, err := json.Marshal(part.Data); err == nil {
				var clean map[string]any
				if json.Unmarshal([]byte(scrub(string(raw))), &clean) == nil {
					local.Parts = append(local.Parts, a2a.DataPart(clean))
					store(".json", "application/json", []byte(scrub(string(raw))))
				}
			}
		case a2a.PartFile:
			if part.File == nil {
				continue
			}
			fileName := truncateSummary(scrub(firstNonEmpty(part.File.Name, name)), 120)
			mediaType := truncateSummary(firstNonEmpty(part.File.MimeType, "application/octet-stream"), 120)
			if data, err := base64.StdEncoding.DecodeString(part.File.Bytes); err == nil && len(data) > 0 {
				// A Go string holds arbitrary bytes, so the same scrub covers a
				// file that carries the token, whatever its type.
				data = []byte(scrub(string(data)))
				if ref, ok := store("-"+safeArtifactName(fileName), mediaType, data); ok {
					local.Parts = append(local.Parts, a2a.FilePart(fileName, mediaType, "inkuna://artifact/"+ref.Digest))
					continue
				}
			}
			local.Parts = append(local.Parts, a2a.Part{Kind: a2a.PartFile, File: &a2a.FileContent{Name: fileName, MimeType: mediaType}, Metadata: map[string]any{"note": "A file the agent referred to by address. Inkuna does not fetch it."}})
		}
	}
	if text.Len() > 0 {
		store(".txt", "text/plain", []byte(text.String()))
	}
	return local, refs, text.String()
}

// safeArtifactName keeps a remote agent's choice of name from being anything
// but a plain file name.
func safeArtifactName(name string) string {
	var builder strings.Builder
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character >= '0' && character <= '9', character == '-', character == '_', character == '.':
			builder.WriteRune(character)
		default:
			builder.WriteRune('-')
		}
		if builder.Len() >= 80 {
			break
		}
	}
	if cleaned := strings.Trim(builder.String(), "-."); cleaned != "" {
		return cleaned
	}
	return "advice"
}

// remoteAgentsByName snapshots the usable remote agents for a run.
func remoteAgentsByName(file config.File) map[string]config.RemoteAgentConfig {
	entries := make(map[string]config.RemoteAgentConfig, len(file.RemoteAgents))
	for _, entry := range file.RemoteAgents {
		if entry.Usable() {
			pin := *entry.Pinned
			entry.Pinned = &pin
			entries[entry.Name] = entry
		}
	}
	return entries
}

// agentRegistry is the project's agents plus the usable remote advisors. It
// also returns the remote agents left out because a local agent has their
// name.
func (service *Service) agentRegistry(repositoryRoot string) (agent.Registry, []string, error) {
	registry, err := agent.Build(repositoryRoot, service.config.AgentDirectories)
	if err != nil {
		return agent.Registry{}, nil, err
	}
	merged, shadowed := registry.WithRemote(service.config.File.RemoteDefinitions())
	return merged, shadowed, nil
}
