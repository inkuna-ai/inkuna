package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/inkuna-ai/inkuna/internal/a2a"
	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/notify"
	agentruntime "github.com/inkuna-ai/inkuna/internal/runtime"
	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/worktree"
)

// workerAssignment carries everything the generic agent handler needs to run
// one A2A task with a headless CLI. It lives for the duration of the task.
type workerAssignment struct {
	id          string
	executionID string
	stepRunID   string
	step        domain.WorkflowStep
	task        domain.TaskSpec
	envelope    domain.ContextEnvelope
	definition  agent.Definition
	// remote is set when the agent is a remote advisor. It comes from the run's
	// own registry and configuration snapshot, never from the bus registration,
	// which is shared between runs whose registries can differ.
	remote      *config.RemoteAgentConfig
	runtimeName domain.Runtime
	workingDir  string
	environment []string
	depth       int
	mu          sync.Mutex
	handoff     *domain.HandoffResult
	pending     *domain.HumanInputRequest
	// servers are the MCP servers the step is granted, with their secrets'
	// values; a peer has none.
	servers []agentruntime.MCPServer
	// userServers are the user's own MCP servers the step is given, as its
	// runtime reported them; pendingSignIn those that need the user to sign
	// in before the first turn, and signIn the sign-in the step is paused
	// on. A peer has none.
	userServers   []agentruntime.UserServer
	pendingSignIn []agentruntime.UserServer
	signIn        *signInRequest
	// grants are the permissions the user approved for this assignment;
	// permission is the approval it is paused on, and asked remembers every
	// grant already put to the user so a denial is never asked about twice.
	grants     []domain.PermissionGrant
	permission *permissionRequest
	asked      map[domain.PermissionGrant]bool
	turns      int
	peerCount  int
	// session is the runtime session the worker's last turn ran in, which
	// the next turn resumes, and delivered counts the human responses and
	// peer messages that session has been sent. previous is the last turn's
	// digest, for a next turn that starts without its session.
	session   string
	delivered struct{ responses, messages int }
	previous  *domain.TurnDigest
	// cachedUntil is when the session's prompt cache expires, after which
	// resuming it would send the whole session again at full price; zero
	// when resuming costs the same at any time.
	cachedUntil time.Time
	// sessionUsage is what the runtime reported the session had used by the
	// end of its last turn, which the next turn's totals will include.
	sessionUsage domain.Usage
	// roundResume is set for a round's target that resumes the session its
	// last run of the step left: what the round's first turn sends in place
	// of the whole envelope, its feedback and the new handoffs of the
	// dependencies that ran again (docs/feedback-loop.md).
	roundResume *domain.ContextEnvelope
	// addressed is the target's latest reply on each feedback item, kept
	// across its turns, and askedItems the questions about them already put
	// to the user, so the same question is never asked twice.
	addressed  map[string]domain.Addressed
	askedItems map[domain.Addressed]bool
	// skillFiles are the selected skills the worker can open, by id, at
	// paths relative to its working directory.
	skillFiles map[string]string
	// parentStep and parentTurn are, for a peer, the step and turn of the
	// worker that asked it, which the peer's usage is counted against.
	parentStep string
	parentTurn int
}

// permissionRequest is a paused approval: the grants it would add, the
// question shown in the inbox, and the handoff of the turn that was denied,
// which stands as the step's result if the user declines.
type permissionRequest struct {
	grants []domain.PermissionGrant
	// asks is what the inbox shows the user, one entry per grant.
	asks []storage.PermissionAsk
	// question is the inbox's heading; detail adds the asks to it, as the
	// worker is told what was approved.
	question string
	detail   string
	handoff  domain.HandoffResult
	// inputID is the inbox request that asks it, by which Inkuna knows who
	// answered.
	inputID string
}

// Inbox options for a permission request. Anything but an approval declines.
const (
	permissionAllow = "Allow for this run"
	permissionDeny  = "Deny"
)

const (
	maxOutputArtifact  = 8 << 20
	maxArtifactPreview = 256 << 10
)

// HandoffWaitingPeers is the handoff status a worker uses to ask other agents
// for help before finishing.
const HandoffWaitingPeers = "waiting-peers"

// registerAgents publishes every worker card on the bus. Registration is
// idempotent; the handler is generic and reads its assignment from the task.
func (service *Service) registerAgents(registry agent.Registry, skillDescriptions map[string]string) {
	for _, definition := range registry.Workers() {
		_ = service.bus.RegisterAgent(definition.Card(skillDescriptions), service.serveAgentTask)
	}
}

func (service *Service) assignment(id string) *workerAssignment {
	service.assignMu.RLock()
	defer service.assignMu.RUnlock()
	return service.assignments[id]
}

func (service *Service) releaseAssignments(executionID string) {
	service.assignMu.Lock()
	defer service.assignMu.Unlock()
	for id, assignment := range service.assignments {
		if assignment.executionID == executionID {
			// A request still waiting when its run ends is never asked about,
			// so who answered it is forgotten with it.
			assignment.mu.Lock()
			if assignment.permission != nil {
				service.takeAnswerer(assignment.permission.inputID)
			}
			assignment.mu.Unlock()
			delete(service.assignments, id)
		}
	}
}

// serveAgentTask is the A2A handler for every worker agent. Each invocation
// is one turn: the first message carries the context envelope, later
// messages carry the user's answers to questions the worker raised.
func (service *Service) serveAgentTask(ctx context.Context, bus *a2a.Bus, task a2a.Task, message a2a.Message) error {
	assignmentID, _ := task.Metadata["assignment"].(string)
	assignment := service.assignment(assignmentID)
	if assignment == nil {
		return fmt.Errorf("no assignment for A2A task %s", task.ID)
	}
	if assignment.remote != nil {
		return service.serveRemoteTask(ctx, bus, task, message, assignment)
	}
	assignment.mu.Lock()
	var declined, allowed *permissionRequest
	refused := ""
	var signIn *signInRequest
	var signInAnswer string
	switch {
	case assignment.signIn != nil && len(task.History) > 1:
		signIn, signInAnswer = assignment.signIn, message.Text()
		assignment.signIn = nil
	case assignment.permission != nil && len(task.History) > 1:
		request := assignment.permission
		assignment.permission = nil
		if permissionApproved(message.Text()) {
			allowed = request
		} else {
			declined = request
			// Nothing is kept for a decline; this forgets one an answer
			// that arrived some other way left.
			service.takeAnswerer(request.inputID)
		}
	case assignment.pending != nil && len(task.History) > 1:
		assignment.envelope.HumanResponses = append(assignment.envelope.HumanResponses, domain.HumanResponse{Question: assignment.pending.Question, Answer: message.Text()})
		assignment.pending = nil
	}
	pending := assignment.pendingSignIn
	assignment.pendingSignIn = nil
	assignment.mu.Unlock()
	if allowed != nil {
		// Asked without the assignment's lock, which a slow policy must not
		// hold. Until it answers, nothing is granted.
		if err := service.decidePermission(ctx, assignment, allowed); err != nil {
			declined, refused = allowed, err.Error()
		} else {
			assignment.mu.Lock()
			assignment.grants = append(assignment.grants, allowed.grants...)
			assignment.envelope.HumanResponses = append(assignment.envelope.HumanResponses, domain.HumanResponse{Question: allowed.detail, Answer: "Approved for this run. Retry what was denied and finish the step."})
			assignment.mu.Unlock()
		}
	}
	switch {
	case signIn != nil:
		if retry := service.answerSignIn(ctx, assignment, signIn, signInAnswer); retry != nil {
			return service.raiseSignIn(ctx, bus, task, assignment, retry)
		}
	case len(pending) > 0 && assignment.depth == 0:
		// Before the first turn, so the step starts with the servers the
		// user signs in to.
		return service.raiseSignIn(ctx, bus, task, assignment, &signInRequest{servers: pending})
	}
	if declined != nil {
		// The user said no, or a policy did: the denied turn's handoff is the
		// step's result.
		warning := "Permission declined; the step finishes without it"
		if refused != "" {
			warning = "Permission was allowed but refused, so the step finishes without it: " + refused
		}
		_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, warning))
		return service.finishAssignment(ctx, bus, task, assignment, declined.handoff)
	}
	for {
		assignment.mu.Lock()
		// Each answer, peer round, or permission retry is another turn. There
		// is no cap on them: a step ends when the worker finishes, the run is
		// cancelled, or a turn exhausts its wall-time budget.
		assignment.turns++
		assignment.mu.Unlock()
		handoff, denials, err := service.runWorker(ctx, assignment)
		var needsSignIn *agentruntime.UserServerError
		if errors.As(err, &needsSignIn) && needsSignIn.SignIn {
			// The CLI stopped before the model ran, so the turn is asked
			// again once the user has signed in or skipped the server.
			server := assignment.takeUserServer(needsSignIn.Server)
			assignment.mu.Lock()
			assignment.turns--
			assignment.mu.Unlock()
			return service.raiseSignIn(ctx, bus, task, assignment, &signInRequest{servers: []agentruntime.UserServer{server}})
		}
		if err != nil {
			return err
		}
		// Each turn replaces the last one's, so when the step ends they are
		// what it was still refused, which a failed run's detail explains
		// and suggests fixes from. A peer's are its asker's business.
		if assignment.depth == 0 {
			if err := service.store.SetStepDenials(service.context(), assignment.stepRunID, denials); err != nil {
				_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, "Could not record what this turn was refused: "+err.Error()))
			}
		}
		// A question about the feedback is asked as any question is, unless
		// the turn waits for its peers first, and then once it is done.
		if questions := service.mergeAddressed(assignment, &handoff); len(questions) > 0 && (handoff.Status != HandoffWaitingPeers || len(handoff.Messages) == 0 || assignment.depth > 0) {
			assignment.mu.Lock()
			handoff.HumanInput = feedbackQuestion(assignment.definition.Name, handoff.HumanInput, questions, assignment.envelope.Feedback.Items)
			for _, question := range questions {
				assignment.askedItems[question] = true
			}
			assignment.mu.Unlock()
		}
		switch {
		case handoff.HumanInput != nil || handoff.Status == domain.HandoffInputRequired:
			question := handoff.HumanInput
			if question == nil {
				question = &domain.HumanInputRequest{Question: handoff.Summary}
			}
			if strings.TrimSpace(question.Question) == "" {
				question.Question = "The agent needs a decision from you but did not phrase a question. Reply with guidance."
			}
			assignment.mu.Lock()
			assignment.pending = question
			assignment.previous = domain.NewTurnDigest(assignment.turns, "a decision from the user", handoff)
			assignment.mu.Unlock()
			input := storage.InputRequest{
				ID: newID("input"), ExecutionID: assignment.executionID, StepID: assignment.step.ID, A2ATaskID: task.ID,
				Agent: task.Agent, AskedBy: assignment.definition.Name, Question: question.Question, Options: question.Options, Context: question.Context,
				State: storage.InputPending, CreatedAt: time.Now().UTC(),
			}
			parts := []a2a.Part{a2a.TextPart(question.Question)}
			if len(question.Options) > 0 {
				parts = append(parts, a2a.DataPart(map[string]any{"options": question.Options, "inputId": input.ID}))
			}
			// Pause the task before the question becomes answerable so the
			// answer always finds a task that accepts follow-ups.
			if err := bus.UpdateStatus(ctx, task.ID, a2a.StateInputRequired, &a2a.Message{Role: a2a.RoleAgent, Parts: parts}); err != nil {
				return err
			}
			return service.raiseInput(input)
		case handoff.Status == HandoffWaitingPeers && len(handoff.Messages) > 0 && assignment.depth == 0:
			replies, err := service.relayPeerMessages(ctx, bus, assignment, task, handoff.Messages)
			if err != nil {
				return err
			}
			assignment.mu.Lock()
			assignment.envelope.PeerMessages = append(assignment.envelope.PeerMessages, replies...)
			assignment.previous = domain.NewTurnDigest(assignment.turns, "replies from "+peerRecipients(handoff.Messages), handoff)
			assignment.mu.Unlock()
			continue
		default:
			// Asked only once the turn is otherwise finished, so a turn that
			// pauses for the user or for peers keeps its denials askable.
			approval := service.permissionRequestFor(assignment, handoff, denials)
			if approval == nil {
				return service.finishAssignment(ctx, bus, task, assignment, handoff)
			}
			input := storage.InputRequest{
				ID: newID("input"), ExecutionID: assignment.executionID, StepID: assignment.step.ID, A2ATaskID: task.ID,
				Agent: task.Agent, AskedBy: assignment.definition.Name, Question: approval.question, Options: []string{permissionAllow, permissionDeny},
				Context: permissionContext(assignment), Permissions: approval.asks, State: storage.InputPending, CreatedAt: time.Now().UTC(),
			}
			approval.inputID = input.ID
			assignment.mu.Lock()
			assignment.permission = approval
			assignment.previous = domain.NewTurnDigest(assignment.turns, "permission for what was denied", handoff)
			assignment.mu.Unlock()
			parts := []a2a.Part{a2a.TextPart(approval.question), a2a.DataPart(map[string]any{"options": input.Options, "inputId": input.ID, "kind": "permission"})}
			if err := bus.UpdateStatus(ctx, task.ID, a2a.StateInputRequired, &a2a.Message{Role: a2a.RoleAgent, Parts: parts}); err != nil {
				return err
			}
			return service.raiseInput(input)
		}
	}
}

// mergeAddressed settles a round target's replies on its feedback items
// against the items it was sent, and puts in the handoff the latest reply on
// each item across the step's turns, since a turn that comes back from the
// user may answer only what it asked about. It returns the questions not yet
// put to the user, a question held back while the step waited for its peers
// included. A reply Go drops is noted on the timeline, and a step that was
// sent no feedback has nothing to reply to.
func (service *Service) mergeAddressed(assignment *workerAssignment, handoff *domain.HandoffResult) []domain.Addressed {
	feedback := assignment.envelope.Feedback
	if feedback == nil {
		handoff.Addressed = nil
		return nil
	}
	settled, reasons := domain.SettleAddressed(feedback.Items, handoff.Addressed)
	for _, reason := range reasons {
		_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, "A reply on the feedback was dropped: "+reason))
	}
	assignment.mu.Lock()
	defer assignment.mu.Unlock()
	if assignment.addressed == nil {
		assignment.addressed = make(map[string]domain.Addressed, len(feedback.Items))
		assignment.askedItems = make(map[domain.Addressed]bool)
	}
	for _, reply := range settled {
		assignment.addressed[reply.Item] = reply
	}
	handoff.Addressed = assignment.repliesLocked()
	var questions []domain.Addressed
	for _, reply := range handoff.Addressed {
		if reply.Status == domain.AddressedQuestion && !assignment.askedItems[reply] {
			questions = append(questions, reply)
		}
	}
	return questions
}

// replies is a round target's latest reply on each feedback item so far,
// in the items' order: what a step that ends without its handoff still
// answered.
func (assignment *workerAssignment) replies() []domain.Addressed {
	assignment.mu.Lock()
	defer assignment.mu.Unlock()
	return assignment.repliesLocked()
}

func (assignment *workerAssignment) repliesLocked() []domain.Addressed {
	feedback := assignment.envelope.Feedback
	if feedback == nil || len(assignment.addressed) == 0 {
		return nil
	}
	replies := make([]domain.Addressed, 0, len(assignment.addressed))
	for _, item := range feedback.Items {
		if reply, ok := assignment.addressed[item.ID]; ok {
			replies = append(replies, reply)
		}
	}
	return replies
}

// feedbackQuestion puts a target's questions about its feedback to the user,
// after any question the turn asked itself, with the items they are about.
func feedbackQuestion(asker string, asked *domain.HumanInputRequest, questions []domain.Addressed, items []domain.FeedbackItem) *domain.HumanInputRequest {
	texts := make(map[string]string, len(items))
	for _, item := range items {
		texts[item.ID] = item.Text
	}
	heading := asker + " has a question about the feedback before it finishes:"
	if len(questions) > 1 {
		heading = asker + " has questions about the feedback before it finishes:"
	}
	lines := []string{heading}
	about := make([]string, 0, len(questions))
	for _, question := range questions {
		lines = append(lines, "["+question.Item+"] "+firstNonEmpty(question.Note, "It did not say what it needs to know; reply with guidance."))
		about = append(about, "["+question.Item+"] "+truncateSummary(texts[question.Item], 300))
	}
	request := &domain.HumanInputRequest{Question: strings.Join(lines, "\n"), Context: "About:\n" + strings.Join(about, "\n")}
	if asked != nil && strings.TrimSpace(asked.Question) != "" {
		request.Question = strings.TrimSpace(asked.Question) + "\n\n" + request.Question
		request.Options = asked.Options
		if context := strings.TrimSpace(asked.Context); context != "" {
			request.Context = context + "\n\n" + request.Context
		}
	}
	return request
}

// finishAssignment records the handoff as the assignment's result and
// publishes it on the A2A task.
func (service *Service) finishAssignment(ctx context.Context, bus *a2a.Bus, task a2a.Task, assignment *workerAssignment, handoff domain.HandoffResult) error {
	assignment.mu.Lock()
	assignment.handoff = &handoff
	assignment.mu.Unlock()
	service.publishHandoff(ctx, bus, task, assignment, handoff)
	if handoff.Status == domain.HandoffFailed {
		return errors.New(firstNonEmpty(handoff.Summary, "agent reported failure"))
	}
	return nil
}

// permissionRequestFor decides whether a turn's denials are worth pausing
// for. It returns nil when nothing new can be granted: the worker is a peer
// (peers stay read-only and never reach the user), the denial is one only the
// step's permission can lift, or the user was already asked about it. Those
// denials are logged so the run view says why a tool call went nowhere.
func (service *Service) permissionRequestFor(assignment *workerAssignment, handoff domain.HandoffResult, denials []domain.PermissionDenial) *permissionRequest {
	if len(denials) == 0 || assignment.depth > 0 {
		return nil
	}
	assignment.mu.Lock()
	defer assignment.mu.Unlock()
	if assignment.asked == nil {
		assignment.asked = map[domain.PermissionGrant]bool{}
	}
	request := &permissionRequest{handoff: handoff}
	lines := []string{}
	for _, denial := range denials {
		grant, ok := agentruntime.GrantFor(denial)
		if !ok && strings.HasPrefix(denial.Tool, "mcp__") {
			_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, fmt.Sprintf("%s was denied and cannot be approved: a step may call only the MCP tools inkuna.yaml lists for its server", denial.Tool)))
			continue
		}
		if !ok {
			_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, fmt.Sprintf("%s was denied and cannot be approved: a %s step decides it (%s)", denial.Tool, assignment.step.Permission, truncateSummary(denial.Detail, 160))))
			continue
		}
		if assignment.asked[grant] {
			continue
		}
		assignment.asked[grant] = true
		request.grants = append(request.grants, grant)
		if grant.Command != "" {
			request.asks = append(request.asks, storage.PermissionAsk{Command: grant.Command})
			lines = append(lines, "• run `"+grant.Command+"`")
		} else {
			request.asks = append(request.asks, storage.PermissionAsk{Tool: grant.Tool, Detail: strings.TrimSpace(denial.Detail)})
			lines = append(lines, "• use "+grant.Tool+prefixed(" — ", denial.Detail))
		}
	}
	if len(request.grants) == 0 {
		return nil
	}
	request.question = permissionHeading(assignment.definition.Name, firstNonEmpty(assignment.step.Title, assignment.step.ID), request.asks)
	request.detail = request.question + "\n" + strings.Join(lines, "\n")
	return request
}

// permissionHeading says in one line who was denied what, and where. The
// commands themselves are listed apart from it: a run-on sentence of shell
// commands is hard to read and does not show where one ends.
func permissionHeading(agentName, step string, asks []storage.PermissionAsk) string {
	commands, tools := 0, 0
	for _, ask := range asks {
		if ask.Command != "" {
			commands++
		} else {
			tools++
		}
	}
	denied := countOf(commands, "command")
	switch {
	case commands == 0:
		denied = countOf(tools, "tool")
	case tools > 0:
		denied += " and " + countOf(tools, "tool")
	}
	them := "them"
	if commands+tools == 1 {
		them = "it"
	}
	return fmt.Sprintf("%s was denied %s while working on %q. Allow %s for this run?", agentName, denied, step, them)
}

// countOf is "a command" for one and "3 commands" for more.
func countOf(count int, noun string) string {
	if count == 1 {
		return "a " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// permissionContext tells the user what an approval covers.
func permissionContext(assignment *workerAssignment) string {
	return fmt.Sprintf("The step runs %s in %s. Approval lasts for this run only and allows exactly what is listed. To allow a command on every run, add it to the stage's commands in .inkuna/rules.yaml. Declining finishes the step with what the agent managed without it.", assignment.step.Permission, assignment.workingDir)
}

func permissionApproved(answer string) bool {
	answer = strings.ToLower(strings.TrimSpace(answer))
	for _, prefix := range []string{"allow", "approve", "yes"} {
		if strings.HasPrefix(answer, prefix) {
			return true
		}
	}
	return false
}

// cacheMargin is how long before its prompt cache expires a session is no
// longer resumed: the CLI takes a moment to start and send its request.
const cacheMargin = 30 * time.Second

// peerRecipients names the agents a worker's messages went to, each once.
func peerRecipients(messages []domain.PeerMessage) string {
	names := make([]string, 0, len(messages))
	for _, message := range messages {
		if name := strings.TrimSpace(message.To); name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func prefixed(prefix, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return prefix + strings.TrimSpace(value)
}

// runWorker executes one headless CLI turn and parses the handoff.
func (service *Service) runWorker(ctx context.Context, assignment *workerAssignment) (domain.HandoffResult, []domain.PermissionDenial, error) {
	current, err := service.dependenciesFor(assignment.executionID).runtime(assignment.runtimeName)
	if err != nil {
		return domain.HandoffResult{}, nil, err
	}
	assignment.mu.Lock()
	envelope := assignment.envelope
	turn := assignment.turns
	grants := append([]domain.PermissionGrant(nil), assignment.grants...)
	session, delivered, previous, cachedUntil, sessionUsage := assignment.session, assignment.delivered, assignment.previous, assignment.cachedUntil, assignment.sessionUsage
	roundResume := assignment.roundResume
	assignment.mu.Unlock()
	// A round's first turn resumes the session the step's last run left.
	resumesRound := roundResume != nil && turn == 1
	if session != "" && !cachedUntil.IsZero() && time.Now().After(cachedUntil.Add(-cacheMargin)) {
		// Resuming now would send the whole session at full price; starting
		// over with the digest sends far less (docs/architecture.md).
		warning := fmt.Sprintf("The last turn's prompt cache expired at %s, so this turn starts over with a digest of it instead of resuming its session", cachedUntil.Local().Format(time.Kitchen))
		if resumesRound {
			warning = fmt.Sprintf("The prompt cache of the step's last session expired at %s, so this round starts a new session with a digest of the step's last run instead of resuming it", cachedUntil.Local().Format(time.Kitchen))
		}
		_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, warning))
		session = ""
	}
	// What this turn delivers, counted before budgets drop or merge entries.
	responses, messages := len(envelope.HumanResponses), len(envelope.PeerMessages)
	// A peer answers in one call, so only a step's own worker keeps its
	// session for a later turn.
	resumable := assignment.depth == 0 && current.Capabilities().Resume
	budgets := service.dependenciesFor(assignment.executionID).budgets
	fullText := service.fullTextFor(ctx, assignment.task.ID, assignment.executionID, assignment.step.ID, assignment.stepRunID, assignment.definition.Name, turn, assignment.skillFiles)
	var prompt []byte
	var sections []domain.PromptSection
	switch {
	case session != "" && resumable && previous != nil:
		// The session holds the rest, so the new answers and replies are all
		// there is to send, held to their own budgets.
		replies, err := domain.ApplyBudgets(domain.ContextEnvelope{HumanResponses: envelope.HumanResponses[delivered.responses:], PeerMessages: envelope.PeerMessages[delivered.messages:], Tracker: envelope.Tracker}, budgets, fullText)
		if err != nil {
			return domain.HandoffResult{}, nil, err
		}
		prompt, sections = continuationPrompt(previous.StoppedFor, replies)
	case session != "" && resumable && resumesRound:
		// The session holds the step's last run, so the round's feedback and
		// what ran again before it are all there is to send.
		resume := *roundResume
		resume.Tracker = envelope.Tracker
		sent, err := domain.ApplyBudgets(resume, budgets, fullText)
		if err != nil {
			return domain.HandoffResult{}, nil, err
		}
		prompt, sections = roundPrompt(sent)
		note := "This round resumes the session of the step's last run, so it is sent only its feedback and what ran again before it"
		if !cachedUntil.IsZero() {
			note += fmt.Sprintf("; that session's prompt cache lasts until %s", cachedUntil.Local().Format(time.Kitchen))
		}
		_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventLifecycle, note))
	default:
		session = ""
		if turn > 1 {
			envelope.PreviousTurn = previous
		}
		// The assignment holds the envelope whole, with each turn's answers
		// and replies added, and each turn is held to the budgets from it.
		if envelope, err = domain.ApplyBudgets(envelope, budgets, fullText); err != nil {
			return domain.HandoffResult{}, nil, err
		}
		prompt, sections = workerPrompt(assignment.definition, envelope)
	}
	stepRunID := assignment.stepRunID
	if turn > 1 {
		stepRunID = fmt.Sprintf("%s-turn%d", assignment.stepRunID, turn)
	}
	service.runsMu.Lock()
	if run := service.runs[assignment.executionID]; run != nil {
		run.active[stepRunID] = current
	}
	service.runsMu.Unlock()
	defer func() {
		service.runsMu.Lock()
		if run := service.runs[assignment.executionID]; run != nil {
			delete(run.active, stepRunID)
		}
		service.runsMu.Unlock()
	}()
	model, effort := "", ""
	if assignment.step.ModelSelection != nil {
		model, effort = assignment.step.ModelSelection.Model, assignment.step.ModelSelection.ReasoningEffort
	}
	request := agentruntime.RunRequest{
		RunID: stepRunID, WorkingDir: assignment.workingDir, Prompt: prompt, Schema: []byte(handoffSchemaFor(envelope.Tracker)), Permission: assignment.step.Permission,
		Budget: assignment.step.Budget, Model: model, ReasoningEffort: effort, Commands: assignment.step.Commands, Grants: grants, Environment: assignment.environment,
		Resumable: resumable, Session: session, SessionUsage: sessionUsage,
	}
	assignment.mu.Lock()
	userServers := append([]agentruntime.UserServer(nil), assignment.userServers...)
	assignment.mu.Unlock()
	if (len(assignment.servers) > 0 || len(userServers) > 0) && assignment.depth == 0 {
		request.MCPServers, request.UserServers = assignment.servers, userServers
		if request.MCPDir, err = filepath.Abs(filepath.Join(service.dataDir, "runs", assignment.executionID, "mcp")); err != nil {
			return domain.HandoffResult{}, nil, fmt.Errorf("locate the run's MCP configuration directory: %w", err)
		}
	}
	if assignment.depth > 0 {
		// A peer answers read-only within ten minutes; its model turns are
		// bounded only when its definition says so.
		request.Permission = domain.PermissionReadOnly
		request.Budget = domain.Budget{WallTimeSeconds: 600, MaxTurns: assignment.definition.MaxTurns, MaxOutputBytes: 2 << 20}
	}
	if request.Budget.WallTimeSeconds <= 0 {
		request.Budget.WallTimeSeconds = 1800
	}
	tracer := service.docReadTracer(assignment)
	sink := func(event domain.RunEvent) error {
		event.RunID = assignment.stepRunID
		if err := service.appendRunEvent(assignment.executionID, event); err != nil {
			return err
		}
		// Tracing is a record of the step, not part of it: a read that
		// cannot be stored is reported and the step goes on.
		if err := tracer.trace(ctx, service, event); err != nil {
			return service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, err.Error()))
		}
		return nil
	}
	call := domain.UsageRecord{
		TaskID: assignment.task.ID, ExecutionID: assignment.executionID, StepID: assignment.step.ID, CallID: stepRunID, Turn: turn, Kind: domain.CallWorker,
		Agent: assignment.definition.Name, Runtime: assignment.runtimeName, Model: model, Sections: sections, StartedAt: time.Now().UTC(),
	}
	if assignment.depth > 0 {
		call.Kind, call.StepID, call.Turn = domain.CallPeer, assignment.parentStep, assignment.parentTurn
	}
	service.runsMu.RLock()
	if run := service.runs[assignment.executionID]; run != nil {
		call.Round = run.round
	}
	service.runsMu.RUnlock()
	// Like its usage, a call's prompt is a record of the call: one that
	// cannot be stored is reported and the call goes on.
	if ref, err := service.storeArtifact(ctx, assignment.task.ID, assignment.executionID, assignment.step.ID, assignment.definition.Name, "prompt", fmt.Sprintf("%s-turn%d-prompt.md", assignment.step.ID, turn), "text/markdown", bytes.NewReader(prompt)); err == nil {
		call.PromptDigest = ref.Digest
	} else {
		_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, "The prompt of "+stepRunID+" was not stored: "+err.Error()))
	}
	service.startCall(assignment.executionID, call)
	result, err := current.Start(ctx, request, sink)
	call.Usage, call.ModelUsed, call.AuthMode, call.Outcome = result.Usage, result.Model, result.AuthMode, agentruntime.Outcome(err)
	if err != nil {
		service.recordUsage(assignment.executionID, assignment.stepRunID, call)
		var failed *agentruntime.UserServerError
		if errors.As(err, &failed) && !failed.SignIn && !result.Acted && ctx.Err() == nil {
			// One of the user's servers did not start and the model never
			// ran: the turn goes again without it.
			_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, failed.Error()+"; the step goes on without it"))
			assignment.dropUserServer(failed.Server)
			return service.runWorker(ctx, assignment)
		}
		if session != "" && call.Outcome == domain.OutcomeFailed && !result.Acted && ctx.Err() == nil {
			// The session could not be resumed, perhaps because it was
			// deleted, and the model never acted: the turn starts over
			// without it. A call that timed out, was cancelled, or failed
			// after the model acted is not retried, so no work is done twice.
			warning := "The last turn's session could not be resumed, so this turn starts over with a digest of it: "
			if resumesRound {
				warning = "The session of the step's last run could not be resumed, so this round starts a new session with a digest of that run: "
			}
			_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, warning+truncateSummary(err.Error(), 300)))
			assignment.mu.Lock()
			assignment.session = ""
			assignment.mu.Unlock()
			return service.runWorker(ctx, assignment)
		}
		return domain.HandoffResult{}, nil, err
	}
	if resumable {
		assignment.mu.Lock()
		assignment.session, assignment.sessionUsage = result.Session, result.SessionUsage
		assignment.delivered.responses, assignment.delivered.messages = responses, messages
		assignment.cachedUntil = result.CacheUntil
		assignment.mu.Unlock()
		// Kept on the step run too, for a later round to resume, which may
		// come after a restart, with nothing of this assignment in memory.
		session := storage.StepSession{Session: result.Session, Runtime: assignment.runtimeName, CacheUntil: result.CacheUntil, Usage: result.SessionUsage}
		if err := service.store.SaveSession(context.WithoutCancel(ctx), assignment.stepRunID, session); err != nil {
			_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, "The step's session was not kept, so a later round cannot resume it: "+err.Error()))
		}
	}
	handoff := parseStepHandoff(result.Output, request.Permission == domain.PermissionReadOnly)
	handoff.Usage = result.Usage
	if handoff.Status == domain.HandoffFailed {
		call.Outcome = domain.OutcomeFailed
	}
	service.recordUsage(assignment.executionID, assignment.stepRunID, call)
	// The stored handoff is among the step's artifacts, whose text bodies a
	// remote advisor downstream may be sent, so it leaves out the replies on
	// the feedback, which restate it (docs/feedback-loop.md#invariants).
	record := handoff
	record.Addressed = nil
	// The document is stored as its own artifact below, and tracker changes
	// are kept apart from the handoff once the step ends (keepTrackerChanges).
	record.Document = ""
	record.TrackerChanges = nil
	if data, err := json.MarshalIndent(record, "", "  "); err == nil {
		if ref, err := service.storeArtifact(ctx, assignment.task.ID, assignment.executionID, assignment.step.ID, assignment.definition.Name, "handoff", fmt.Sprintf("%s-turn%d-handoff.json", assignment.step.ID, turn), "application/json", strings.NewReader(string(data))); err == nil {
			handoff.Artifacts = append(handoff.Artifacts, ref)
		}
	}
	// Only the outputs Inkuna stored reach the steps that depend on this one.
	stored := make([]domain.ArtifactOutput, 0, len(handoff.Outputs))
	for _, output := range handoff.Outputs {
		ref, err := service.ingestOutput(ctx, assignment, output)
		if err != nil {
			_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventWarning, "Output "+output.Path+" was not stored: "+err.Error()))
			continue
		}
		handoff.Artifacts = append(handoff.Artifacts, ref)
		output.Path = filepath.ToSlash(filepath.Clean(output.Path))
		output.Digest = ref.Digest
		stored = append(stored, output)
	}
	handoff.Outputs = stored
	service.keepDocument(ctx, assignment, call.Round, &handoff)
	return handoff, result.Denials, nil
}

// relayPeerMessages sends each worker request to the named peer as a new
// A2A task in the same context, waits for the replies, and returns them as
// compact peer messages. Peers run read-only and cannot ask further peers.
func (service *Service) relayPeerMessages(ctx context.Context, bus *a2a.Bus, assignment *workerAssignment, parent a2a.Task, messages []domain.PeerMessage) ([]domain.PeerMessage, error) {
	service.runsMu.RLock()
	run := service.runs[assignment.executionID]
	service.runsMu.RUnlock()
	if run == nil {
		return nil, fmt.Errorf("run %q was not found", assignment.executionID)
	}
	replies := make([]domain.PeerMessage, 0, len(messages))
	for _, request := range messages {
		target, ok := run.registry.Get(request.To)
		// A remote advisor only ever receives the advisor handoff of a stage a
		// rule or the plan gave it, never text a worker composed.
		if !ok || target.Remote() || request.To == agent.Orchestrator || request.To == assignment.definition.Name {
			replies = append(replies, domain.PeerMessage{From: "inkuna", To: assignment.definition.Name, Subject: "Undeliverable", Content: fmt.Sprintf("Agent %q is not available for peer requests.", request.To)})
			continue
		}
		if strings.TrimSpace(request.Content) == "" {
			continue
		}
		// The request, under its subject, is the peer's objective, so it is
		// not repeated as a peer message; the criterion says who asked. The
		// asker's stage stays with the asker, but the task's constraints bind
		// a peer's advice too.
		objective := request.Content
		if subject := strings.TrimSpace(request.Subject); subject != "" {
			objective = subject + "\n\n" + objective
		}
		peerEnvelope := domain.ContextEnvelope{
			Version: domain.ContractVersion, TaskID: assignment.task.ID, StepID: assignment.step.ID + "-peer-" + target.Name, Runtime: assignment.runtimeName,
			Agent: target.Name, AgentInstructions: target.Instructions, Objective: objective,
			AcceptanceCriteria: []string{"Answer " + assignment.definition.Name + "'s request concisely in the handoff summary"},
			Constraints:        assignment.task.Constraints,
			Budget:             domain.Budget{WallTimeSeconds: 600, MaxTurns: target.MaxTurns},
			Metadata:           map[string]string{domain.MetadataPermission: string(domain.PermissionReadOnly)},
		}
		if target.Runtime != "" {
			peerEnvelope.Runtime = target.Runtime
		}
		environment, err := secretEnvironment(run.deps, peerEnvelope.Runtime, target)
		if err != nil {
			return nil, err
		}
		peerStep := assignment.step
		peerStep.ID = peerEnvelope.StepID
		peerStep.Agent = target.Name
		peerStep.Permission = domain.PermissionReadOnly
		peerStep.AllowedPaths = nil
		peerStep.Runtime = peerEnvelope.Runtime
		if target.Model != "" {
			peerStep.ModelSelection = &domain.ModelSelection{Model: target.Model, ReasoningEffort: target.ReasoningEffort, Source: "override", Rationale: "agent pins its model"}
		}
		assignment.mu.Lock()
		assignment.peerCount++
		peerRunID := fmt.Sprintf("%s-peer%d-%s", assignment.stepRunID, assignment.peerCount, target.Name)
		parentTurn := assignment.turns
		assignment.mu.Unlock()
		peer := &workerAssignment{
			id: newID("assignment"), executionID: assignment.executionID, stepRunID: peerRunID, step: peerStep, task: assignment.task,
			envelope: peerEnvelope, definition: target, runtimeName: peerEnvelope.Runtime, workingDir: assignment.workingDir, environment: environment, depth: assignment.depth + 1,
			parentStep: assignment.step.ID, parentTurn: parentTurn,
		}
		service.assignMu.Lock()
		service.assignments[peer.id] = peer
		service.assignMu.Unlock()
		data, err := envelopeData(peerEnvelope)
		if err != nil {
			return nil, err
		}
		_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventMessage, fmt.Sprintf("%s asked %s: %s", assignment.definition.Name, target.Name, truncateSummary(request.Content, 160))))
		service.runsMu.RLock()
		round := run.round
		service.runsMu.RUnlock()
		if err := service.store.StartRun(ctx, peer.stepRunID, assignment.executionID, run.spec.ID, assignment.step.ID, round, peer.runtimeName, assignment.workingDir); err != nil {
			return nil, fmt.Errorf("start peer run: %w", err)
		}
		peerTask, err := bus.SendMessage(run.ctx, target.Name, a2a.MessageSendParams{
			Message:  a2a.Message{Role: a2a.RoleAgent, ContextID: parent.ContextID, ReferenceTaskIDs: []string{parent.ID}, Parts: []a2a.Part{a2a.TextPart(request.Content), a2a.DataPart(data)}},
			Metadata: map[string]any{"assignment": peer.id, "executionId": assignment.executionID, "stepId": peerStep.ID, "parentTask": parent.ID},
		})
		if err != nil {
			return nil, err
		}
		service.runsMu.Lock()
		run.a2aTasks = append(run.a2aTasks, peerTask.ID)
		service.runsMu.Unlock()
		final, err := bus.WaitTerminal(ctx, peerTask.ID)
		if err != nil {
			// Record what actually happened to the peer task: cancelled when the
			// cancel took effect, its real terminal state if it had already
			// finished, or failed when it could not be cancelled at all.
			state, summary := "cancelled", "Peer request cancelled"
			cancelled, cancelErr := bus.CancelTask(context.Background(), peerTask.ID)
			switch {
			case cancelErr != nil:
				state, summary = "failed", "Peer request abandoned and could not be cancelled: "+cancelErr.Error()
				err = fmt.Errorf("%w (peer task %s could not be cancelled: %w)", err, peerTask.ID, cancelErr)
			case cancelled.Status.State != a2a.StateCanceled:
				state, summary = string(cancelled.Status.State), "Peer request finished as "+string(cancelled.Status.State)+" while the wait was abandoned"
			}
			_ = service.store.FinishRun(context.Background(), peer.stepRunID, state, domain.HandoffResult{Version: domain.ContractVersion, Status: state, Summary: summary})
			return nil, err
		}
		peer.mu.Lock()
		handoff := peer.handoff
		peer.mu.Unlock()
		peerState := "completed"
		if final.Status.State != a2a.StateCompleted {
			peerState = "failed"
		}
		recorded := domain.HandoffResult{Version: domain.ContractVersion, Status: peerState, Summary: "Peer request " + string(final.Status.State)}
		if handoff != nil {
			recorded = *handoff
		}
		_ = service.store.FinishRun(context.Background(), peer.stepRunID, peerState, recorded)
		reply := domain.PeerMessage{From: target.Name, To: assignment.definition.Name}
		if request.Subject != "" {
			reply.Subject = "Re: " + request.Subject
		}
		switch {
		case final.Status.State == a2a.StateCompleted && handoff != nil:
			reply.Content = handoff.Summary
			if len(handoff.Decisions) > 0 {
				reply.Content += "\nDecisions: " + strings.Join(handoff.Decisions, "; ")
			}
			if len(handoff.Risks) > 0 {
				reply.Content += "\nRisks: " + strings.Join(handoff.Risks, "; ")
			}
		case final.Status.State == a2a.StateCompleted:
			reply.Content = "Peer finished without a structured reply."
		default:
			reply.Content = "Peer request " + string(final.Status.State)
			if final.Status.Message != nil {
				reply.Content += ": " + final.Status.Message.Text()
			}
		}
		_ = service.appendRunEvent(assignment.executionID, syntheticEvent(assignment.stepRunID, 0, domain.EventMessage, fmt.Sprintf("%s replied to %s: %s", target.Name, assignment.definition.Name, truncateSummary(reply.Content, 160))))
		replies = append(replies, reply)
	}
	return replies, nil
}

// publishHandoff mirrors the handoff onto the A2A task as artifacts so other
// agents and the UI can discover outputs without reading transcripts.
func (service *Service) publishHandoff(ctx context.Context, bus *a2a.Bus, task a2a.Task, assignment *workerAssignment, handoff domain.HandoffResult) {
	data := map[string]any{
		"status": handoff.Status, "changes": len(handoff.Changes), "decisions": handoff.Decisions, "risks": handoff.Risks, "verification": handoff.Verification,
	}
	// A round's target replies on its feedback in its handoff artifact.
	if len(handoff.Addressed) > 0 {
		data["addressed"] = handoff.Addressed
	}
	parts := []a2a.Part{a2a.TextPart(handoff.Summary), a2a.DataPart(data)}
	service.runsMu.RLock()
	round := 0
	if run := service.runs[assignment.executionID]; run != nil {
		round = run.round
	}
	service.runsMu.RUnlock()
	for _, ref := range handoff.Artifacts {
		part := a2a.FilePart(ref.Name, ref.MediaType, "inkuna://artifact/"+ref.Digest)
		// What a link to it shows before it is opened.
		part.Metadata = map[string]any{"kind": ref.Kind, "size": ref.Size}
		if round > 0 {
			part.Metadata["round"] = round
		}
		parts = append(parts, part)
	}
	_ = bus.AddArtifact(ctx, task.ID, a2a.Artifact{Name: "handoff", Description: "Compact handoff from " + assignment.definition.Name, Parts: parts})
}

// serveHumanTask is the A2A handler for the user. A first message is a
// question that lands in the inbox; a follow-up message is the answer.
func (service *Service) serveHumanTask(ctx context.Context, bus *a2a.Bus, task a2a.Task, message a2a.Message) error {
	if len(task.History) <= 1 {
		executionID, _ := task.Metadata["executionId"].(string)
		stepID, _ := task.Metadata["stepId"].(string)
		askedBy, _ := task.Metadata["askedBy"].(string)
		options := []string{}
		about := ""
		for _, part := range message.Parts {
			if part.Kind == a2a.PartData {
				if raw, ok := part.Data["options"].([]any); ok {
					for _, option := range raw {
						if text, ok := option.(string); ok {
							options = append(options, text)
						}
					}
				}
				if text, ok := part.Data["context"].(string); ok {
					about = text
				}
			}
		}
		input := storage.InputRequest{
			ID: newID("input"), ExecutionID: executionID, StepID: stepID, A2ATaskID: task.ID, Agent: agent.Human, AskedBy: askedBy,
			Question: message.Text(), Options: options, Context: about, State: storage.InputPending, CreatedAt: time.Now().UTC(),
		}
		input.Review, _ = task.Metadata["review"].(bool)
		input.ReviewTarget, _ = task.Metadata["reviewTarget"].(string)
		input.CheckPause, _ = task.Metadata["checkPause"].(bool)
		input.Stage, _ = task.Metadata["stage"].(bool)
		waiting := &a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.TextPart("Waiting in the Inkuna inbox")}}
		if input.Review {
			// The run turns reviewing once the task is waiting, so the
			// question is in the inbox first. A review is answered through
			// ApproveReview or RequestChanges, never as a follow-up message.
			if err := service.store.SaveInput(context.Background(), input); err != nil {
				return err
			}
			service.emit("inkuna:inbox", input.ID)
			return bus.UpdateStatus(ctx, task.ID, a2a.StateInputRequired, waiting)
		}
		if err := bus.UpdateStatus(ctx, task.ID, a2a.StateInputRequired, waiting); err != nil {
			return err
		}
		return service.raiseInput(input)
	}
	return bus.AddArtifact(ctx, task.ID, a2a.Artifact{Name: "answer", Description: "Answer from the user", Parts: []a2a.Part{a2a.TextPart(message.Text())}})
}

// raiseInput records a question, flips the run to waiting, and notifies.
func (service *Service) raiseInput(input storage.InputRequest) error {
	if err := service.store.SaveInput(context.Background(), input); err != nil {
		return err
	}
	service.updateRun(input.ExecutionID, func(view *WorkflowRunView) {
		view.State = "waiting"
		view.PendingInputs++
		for index := range view.Steps {
			if view.Steps[index].ID == input.StepID {
				view.Steps[index].State = "waiting"
			}
		}
	})
	service.persistRun(input.ExecutionID)
	_ = service.appendRunEvent(input.ExecutionID, syntheticEvent(service.liveStepRunID(input.ExecutionID, input.StepID), 0, domain.EventWarning, "Input required: "+truncateSummary(input.Question, 200)))
	_ = service.dependenciesFor(input.ExecutionID).notifier.Notify(notify.Notification{Title: "Inkuna needs your input", Body: input.Question, Kind: "input-required", Reference: input.ID})
	service.emit("inkuna:inbox", input.ID)
	service.emit("inkuna:runs", input.ExecutionID)
	return nil
}

// commitStep records the worker's changes on the task branch. It never
// pushes; the commit exists so the user can inspect work step by step.
func (service *Service) commitStep(ctx context.Context, run *liveExecution, step domain.WorkflowStep, stepRunID string, handoff *domain.HandoffResult) {
	service.runsMu.RLock()
	path, round := run.view.Worktree, run.round
	service.runsMu.RUnlock()
	if path == "" {
		return
	}
	subject := fmt.Sprintf("inkuna(%s): %s", step.ID, truncateSummary(firstNonEmpty(handoff.Summary, step.Title), 72))
	if round > 1 {
		subject = fmt.Sprintf("inkuna(%s): %s (round %d)", step.ID, truncateSummary(firstNonEmpty(handoff.Summary, step.Title), 72), round)
	}
	body := fmt.Sprintf("%s\n\nAgent: %s\nTask: %s\nRun: %s", subject, step.Agent, run.task.ID, run.view.ID)
	commit, ok, err := worktree.CommitAll(ctx, path, body)
	if err != nil {
		_ = service.appendRunEvent(run.view.ID, syntheticEvent(stepRunID, 0, domain.EventWarning, "Commit failed: "+err.Error()))
		return
	}
	if !ok {
		_ = service.appendRunEvent(run.view.ID, syntheticEvent(stepRunID, 0, domain.EventLifecycle, "No file changes to commit"))
		return
	}
	view := &CommitView{Hash: commit.Hash, ShortHash: commit.ShortHash, Author: commit.Author, Date: commit.Date, Subject: commit.Subject, Body: commit.Body, Stat: commit.Stat, StepID: step.ID}
	service.updateRun(run.view.ID, func(current *WorkflowRunView) { current.LastCommit = view })
	_ = service.store.SaveCommit(context.Background(), storage.CommitRecord{ExecutionID: run.view.ID, StepID: step.ID, Round: round, Agent: step.Agent, Hash: commit.Hash, Subject: commit.Subject, Stat: commit.Stat})
	if commit.Diff != "" {
		if ref, err := service.storeArtifact(ctx, run.task.ID, run.view.ID, step.ID, step.Agent, "diff", step.ID+".diff", "text/x-diff", strings.NewReader(commit.Diff)); err == nil {
			handoff.Artifacts = append(handoff.Artifacts, ref)
		}
	}
	_ = service.appendRunEvent(run.view.ID, syntheticEvent(stepRunID, 0, domain.EventFile, "Committed "+commit.ShortHash+": "+commit.Subject))
}

// storeArtifact writes a blob to the content-addressed store and indexes it
// for the task so every agent can find it later.
func (service *Service) storeArtifact(ctx context.Context, taskID, executionID, stepID, agentName, kind, name, mediaType string, reader io.Reader) (domain.ArtifactRef, error) {
	if service.artifacts == nil {
		return domain.ArtifactRef{}, errors.New("artifact store is not initialized")
	}
	service.artifactMu.RLock()
	defer service.artifactMu.RUnlock()
	ref, err := service.artifacts.Put(name, mediaType, reader)
	if err != nil {
		return domain.ArtifactRef{}, err
	}
	ref.Kind = kind
	record := storage.ArtifactRecord{Digest: ref.Digest, TaskID: taskID, ExecutionID: executionID, StepID: stepID, Agent: agentName, Kind: kind, Name: name, MediaType: mediaType, Size: ref.Size}
	if err := service.store.RecordArtifact(context.WithoutCancel(ctx), record); err != nil {
		return domain.ArtifactRef{}, err
	}
	return ref, nil
}

// ingestOutput copies a file the worker declared into the artifact store.
// Paths must stay inside the working directory.
func (service *Service) ingestOutput(ctx context.Context, assignment *workerAssignment, output domain.ArtifactOutput) (domain.ArtifactRef, error) {
	clean := filepath.Clean(output.Path)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return domain.ArtifactRef{}, fmt.Errorf("output path %q escapes the working directory", output.Path)
	}
	// os.Root confines every lookup and open to the working directory: a
	// symlink anywhere in the path that points outside the root fails at open
	// time, so there is no window between validation and reading.
	root, err := os.OpenRoot(assignment.workingDir)
	if err != nil {
		return domain.ArtifactRef{}, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(clean)
	if err != nil {
		return domain.ArtifactRef{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return domain.ArtifactRef{}, fmt.Errorf("output %q is a symbolic link", output.Path)
	}
	if info.IsDir() {
		return domain.ArtifactRef{}, fmt.Errorf("output %q is a directory", output.Path)
	}
	if info.Size() > maxOutputArtifact {
		return domain.ArtifactRef{}, fmt.Errorf("output %q exceeds %d bytes", output.Path, maxOutputArtifact)
	}
	file, err := root.Open(clean)
	if err != nil {
		return domain.ArtifactRef{}, fmt.Errorf("output %q could not be opened inside the working directory: %w", output.Path, err)
	}
	defer func() { _ = file.Close() }()
	name := output.Name
	if name == "" {
		name = filepath.Base(clean)
	}
	mediaType := output.MediaType
	if mediaType == "" {
		mediaType = mediaTypeFor(clean)
	}
	ref, err := service.storeArtifact(ctx, assignment.task.ID, assignment.executionID, assignment.step.ID, assignment.definition.Name, "output", name, mediaType, file)
	if err != nil {
		return domain.ArtifactRef{}, err
	}
	if output.Description != "" {
		_ = service.store.RecordArtifact(context.WithoutCancel(ctx), storage.ArtifactRecord{Digest: ref.Digest, TaskID: assignment.task.ID, ExecutionID: assignment.executionID, StepID: assignment.step.ID, Agent: assignment.definition.Name, Kind: "output", Name: name, MediaType: mediaType, Size: ref.Size, Description: output.Description})
	}
	return ref, nil
}

// secretEnvironment resolves the secrets declared for a runtime and agent.
// Undeclared names are ignored so an agent file cannot exfiltrate arbitrary
// environment variables.
func secretEnvironment(deps runDependencies, runtimeName domain.Runtime, definition agent.Definition) ([]string, error) {
	declared := make(map[string]bool)
	for _, name := range deps.secretNames {
		declared[name] = true
	}
	names := make([]string, 0)
	seen := make(map[string]bool)
	for _, name := range append(append([]string(nil), deps.runtimeSecrets[runtimeName]...), definition.Secrets...) {
		if declared[name] && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	environment, err := deps.secrets.Environment(names)
	if err != nil {
		return nil, err
	}
	// The runtime hides these values in the events and handoff it keeps,
	// as it does a server's secrets, and cannot hide one this short.
	for _, pair := range environment {
		if name, value, ok := strings.Cut(pair, "="); ok && value != "" && len(value) < agentruntime.MinMCPSecretBytes {
			return nil, fmt.Errorf("secret %s is shorter than %d bytes, too short for Inkuna to hide in the events and output it keeps; store a full credential", name, agentruntime.MinMCPSecretBytes)
		}
	}
	return environment, nil
}

// mcpGrant resolves a step's MCP servers from the run's configuration
// snapshot: what the runtime starts, with the secrets' values, and what the
// envelope records, without them. A server no longer declared, or a secret
// that is not set, stops the step rather than let it run without the server,
// and so does a secret too short for the runtime to hide in what Inkuna
// keeps.
func mcpGrant(deps runDependencies, step domain.WorkflowStep) ([]agentruntime.MCPServer, []domain.MCPServer, error) {
	if len(step.MCPServers) == 0 {
		return nil, nil, nil
	}
	servers := make([]agentruntime.MCPServer, 0, len(step.MCPServers))
	briefs := make([]domain.MCPServer, 0, len(step.MCPServers))
	for _, name := range step.MCPServers {
		entry, ok := deps.mcpServers[name]
		if !ok {
			return nil, nil, fmt.Errorf("step %q is granted MCP server %q, which inkuna.yaml no longer declares", step.ID, name)
		}
		resolve := func(secret string) (string, error) {
			value, _, err := deps.secrets.Resolve(secret)
			if err != nil {
				return "", fmt.Errorf("read secret %s for MCP server %q: %w", secret, name, err)
			}
			if value == "" {
				return "", fmt.Errorf("MCP server %q needs secret %s, which is set in neither the keychain nor the environment", name, secret)
			}
			if len(value) < agentruntime.MinMCPSecretBytes {
				return "", fmt.Errorf("MCP server %q's secret %s is shorter than %d bytes, too short for Inkuna to hide in the events and output it keeps; store a full credential", name, secret, agentruntime.MinMCPSecretBytes)
			}
			return value, nil
		}
		server := agentruntime.MCPServer{Name: name, Command: append([]string(nil), entry.Command...), URL: entry.URL, Tools: append([]string(nil), entry.Tools...)}
		if len(entry.Env) > 0 {
			server.Env = make(map[string]string, len(entry.Env))
			for variable, secret := range entry.Env {
				value, err := resolve(secret)
				if err != nil {
					return nil, nil, err
				}
				server.Env[variable] = value
			}
		}
		if entry.Bearer != "" {
			value, err := resolve(entry.Bearer)
			if err != nil {
				return nil, nil, err
			}
			server.Bearer = value
		}
		servers = append(servers, server)
		briefs = append(briefs, entry.Brief())
	}
	return servers, briefs, nil
}

// mediaTypeFor guesses a media type, preferring text types for the source
// and document extensions agents most often produce so previews work.
func mediaTypeFor(path string) string {
	extension := strings.ToLower(filepath.Ext(path))
	known := map[string]string{
		".md": "text/markdown", ".txt": "text/plain", ".log": "text/plain", ".go": "text/x-go", ".ts": "text/typescript", ".tsx": "text/typescript",
		".js": "text/javascript", ".json": "application/json", ".yaml": "application/yaml", ".yml": "application/yaml", ".toml": "text/plain",
		".diff": "text/x-diff", ".patch": "text/x-diff", ".csv": "text/csv", ".sql": "text/plain", ".sh": "text/plain", ".py": "text/x-python",
		".rs": "text/x-rust", ".html": "text/html", ".css": "text/css", ".xml": "application/xml", ".mermaid": "text/plain", ".mmd": "text/plain",
	}
	if mediaType, ok := known[extension]; ok {
		return mediaType
	}
	if mediaType := mime.TypeByExtension(extension); mediaType != "" {
		return mediaType
	}
	return "application/octet-stream"
}

func peerNames(registry agent.Registry, self string) []string {
	names := make([]string, 0)
	for _, definition := range registry.Workers() {
		if definition.Name != self && !definition.Remote() {
			names = append(names, definition.Name)
		}
	}
	return names
}

func envelopeData(envelope domain.ContextEnvelope) (map[string]any, error) {
	raw, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	return data, nil
}

// parseStepHandoff decodes a step's handoff (parseHandoff). A step that
// writes and returned nothing usable still completes, since its commit is
// what the checks after it inspect. A read-only step changes nothing, so its
// handoff is all it produces: one it did not return, or that could not be
// decoded, fails the step, so a review that said nothing never counts as an
// approval.
func parseStepHandoff(output json.RawMessage, readOnly bool) domain.HandoffResult {
	handoff := parseHandoff(output)
	if readOnly && (len(output) == 0 || json.Unmarshal(output, new(domain.HandoffResult)) != nil) {
		handoff.Status = domain.HandoffFailed
		handoff.Summary += ". A read-only step's handoff is its whole result, so the step fails rather than pass without one"
	}
	return handoff
}

// parseHandoff decodes the worker's structured output. A worker that
// produced nothing usable still completes so the pipeline can continue to
// verification, which is where correctness is actually enforced; see
// parseStepHandoff for the read-only steps it does not suit.
func parseHandoff(output json.RawMessage) domain.HandoffResult {
	if len(output) == 0 {
		return domain.HandoffResult{Version: domain.ContractVersion, Status: domain.HandoffCompleted, Summary: "Worker finished without a structured handoff"}
	}
	var handoff domain.HandoffResult
	if err := json.Unmarshal(output, &handoff); err != nil {
		return domain.HandoffResult{Version: domain.ContractVersion, Status: domain.HandoffCompleted, Summary: "Worker handoff could not be decoded: " + err.Error()}
	}
	if handoff.Version == 0 {
		handoff.Version = domain.ContractVersion
	}
	handoff.Status = strings.ToLower(strings.TrimSpace(handoff.Status))
	switch handoff.Status {
	case domain.HandoffCompleted, domain.HandoffFailed, domain.HandoffInputRequired, HandoffWaitingPeers:
	case "success", "done", "complete", "":
		handoff.Status = domain.HandoffCompleted
	case "error", "failure":
		handoff.Status = domain.HandoffFailed
	case "needs-input", "question", "blocked":
		handoff.Status = domain.HandoffInputRequired
	default:
		handoff.Status = domain.HandoffCompleted
	}
	if strings.TrimSpace(handoff.Summary) == "" {
		handoff.Summary = "Worker returned no summary"
	}
	// Artifacts are what Inkuna stored, never what a worker names: each
	// becomes a link that opens it.
	handoff.Artifacts = nil
	return handoff
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

const handoffSchema = `{
  "type": "object",
  "required": ["version", "status", "summary"],
  "properties": {
    "version": {"type": "integer"},
    "status": {"type": "string", "enum": ["completed", "failed", "input-required", "waiting-peers"]},
    "summary": {"type": "string"},
    "changes": {"type": "array", "items": {"type": "object", "properties": {"path": {"type": "string"}, "summary": {"type": "string"}}}},
    "verification": {"type": "array", "items": {"type": "string"}},
    "decisions": {"type": "array", "items": {"type": "string"}},
    "risks": {"type": "array", "items": {"type": "string"}},
    "document": {"type": "string"},
    "outputs": {"type": "array", "items": {"type": "object", "required": ["path"], "properties": {"path": {"type": "string"}, "name": {"type": "string"}, "mediaType": {"type": "string"}, "description": {"type": "string"}}}},
    "messages": {"type": "array", "items": {"type": "object", "required": ["to", "content"], "properties": {"to": {"type": "string"}, "subject": {"type": "string"}, "content": {"type": "string"}}}},
    "humanInput": {"type": "object", "required": ["question"], "properties": {"question": {"type": "string"}, "options": {"type": "array", "items": {"type": "string"}}, "context": {"type": "string"}}},
    "addressed": {"type": "array", "items": {"type": "object", "required": ["item", "status"], "properties": {"item": {"type": "string"}, "status": {"type": "string", "enum": ["fixed", "declined", "question"]}, "note": {"type": "string"}}}}
  },
  "additionalProperties": true
}`
