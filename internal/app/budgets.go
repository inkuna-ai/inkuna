package app

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/skill"
)

// FullTextArtifact is the artifact kind holding the whole of an envelope
// part a budget cut, for the user to read in the Artifacts tab.
const FullTextArtifact = "full-text"

// skillBrief is what a run keeps about a skill besides its body.
type skillBrief struct {
	description string
	onDemand    bool
}

// skillFiles maps each skill a worker can open to its path relative to the
// working directory: the repository's .inkuna/skills/<id>/SKILL.md, when the
// file there holds the body Inkuna loaded at planning (a worktree is made
// from a commit and can hold an older one) and lies inside the step's
// allowed paths, or anywhere when the step names none.
func skillFiles(workingDir string, allowed []string, bodies map[string]string) map[string]string {
	files := make(map[string]string, len(bodies))
	for id, body := range bodies {
		relative := path.Join(".inkuna", "skills", id, "SKILL.md")
		if !insideAllowed(relative, allowed) {
			continue
		}
		if found, err := skill.Load(filepath.Join(workingDir, ".inkuna", "skills", id)); err == nil && found.Body == body {
			files[id] = relative
		}
	}
	return files
}

// insideAllowed reports whether a slash-separated path relative to the
// working directory lies inside one of the allowed paths.
func insideAllowed(relative string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, entry := range allowed {
		root := path.Clean(filepath.ToSlash(strings.TrimSpace(entry)))
		if root == "." || relative == root || strings.HasPrefix(relative, root+"/") {
			return true
		}
	}
	return false
}

// onDemandSkills lists, by id, the skills whose manifest asks to be sent by
// reference and whose file the worker can open. The others go inline.
func onDemandSkills(briefs map[string]skillBrief, files map[string]string) []domain.SkillBrief {
	sent := make([]domain.SkillBrief, 0)
	for id, file := range files {
		if brief := briefs[id]; brief.onDemand {
			sent = append(sent, domain.SkillBrief{ID: id, Description: brief.description, Path: file})
		}
	}
	sort.Slice(sent, func(a, b int) bool { return sent[a].ID < sent[b].ID })
	return sent
}

// fullTextFor says where a part a budget cuts can be read whole: a skill's
// file when the worker can open it, otherwise an artifact stored for the
// user. Like a call's prompt, the full text is a record: one that cannot be
// stored is reported on the step's timeline, and the part is cut with a note
// that names no place.
func (service *Service) fullTextFor(ctx context.Context, taskID, executionID, stepID, stepRunID, agentName string, turn int, files map[string]string) domain.FullText {
	return func(part, text string) string {
		if id, ok := strings.CutPrefix(part, domain.SectionSkill+":"); ok && files[id] != "" {
			return files[id]
		}
		name := fmt.Sprintf("%s-turn%d-%s.md", stepID, turn, strings.ReplaceAll(part, ":", "-"))
		ref, err := service.storeArtifact(ctx, taskID, executionID, stepID, agentName, FullTextArtifact, name, "text/markdown", strings.NewReader(text))
		if err != nil {
			_ = service.appendRunEvent(executionID, syntheticEvent(stepRunID, 0, domain.EventWarning, fmt.Sprintf("The full text of %s was cut to fit and could not be stored, so its note names no place to read it: %v", part, err)))
			return ""
		}
		return "inkuna://artifact/" + ref.Digest
	}
}
