package todo

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Import reads a TODO.*.md and returns its tasks. The database becomes the source of truth after
// this runs once — Import is the migration, not a sync — so it is deliberately forgiving: a line it
// cannot fully decompose still becomes a task carrying its whole text, because losing a task is
// worse than losing a field of one.
//
// defaultTag, when given, is stamped on every task of the file and prefixes the minted ids — it is
// how one file's tasks stay distinguishable from another's (the horchestra convention imports
// TODO.ce.md under "ce" and TODO.ee.md under "ee"). A `**P2/EE**`-style suffix in the old edition
// grammar becomes a tag too, as does an explicit `· tags: a, b` tail.
func Import(path, defaultTag string) ([]Task, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var (
		tasks   []Task
		epic    string
		seq     = map[string]int{} // per-epic counter, for the id
		cur     *Task
		curBody []string
		order   int
	)
	flush := func() {
		if cur == nil {
			return
		}
		cur.Text = strings.TrimSpace(strings.Join(curBody, "\n"))
		decorate(cur)
		tasks = append(tasks, *cur)
		cur, curBody = nil, nil
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024) // a task's prose can be long
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "## "):
			flush()
			epic = epicName(strings.TrimPrefix(line, "## "))
		case strings.HasPrefix(line, "### "):
			// A sub-heading stays within its epic but ends the current task's body.
			flush()
		case taskStart.MatchString(line):
			flush()
			order++
			slug := slugFor(epic)
			seq[slug]++
			id := fmt.Sprintf("%s-%02d", slug, seq[slug])
			var tags []string
			if len(defaultTag) > 0 {
				id = strings.ToLower(defaultTag) + "-" + id
				tags = append(tags, strings.ToLower(defaultTag))
			}
			cur = &Task{
				ID:    id,
				Tags:  tags,
				Epic:  epic,
				Order: order,
			}
			if strings.HasPrefix(strings.TrimSpace(line), "- [x]") || strings.HasPrefix(strings.TrimSpace(line), "- [X]") {
				cur.Status = StatusDone
			} else {
				cur.Status = StatusOpen
			}
			curBody = []string{stripCheckbox(line)}
		default:
			if cur != nil {
				curBody = append(curBody, line)
			}
		}
	}
	flush()
	return tasks, sc.Err()
}

var (
	taskStart  = regexp.MustCompile(`^\s*- \[[ xX]\] `)
	boldTag    = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	priTag     = regexp.MustCompile(`^(P[0-5]|E[0-9]|v2|EE)(/EE)?$`)
	doneTag    = regexp.MustCompile(`\*\*DONE(?:\s+([0-9a-f]{7,40}))?\*?:?\*?\*?`)
	emDashTail = regexp.MustCompile(`—\s*([a-z0-9-]+)(?:\s|$|·)`)
	parenTail  = regexp.MustCompile(`\(([^()]*(?:/[^()]*)+|[a-z][a-zA-Z0-9_./, -]*)\)\s*—`)
	depTail    = regexp.MustCompile(`·\s*dep:\s*(.+?)\s*(?:·\s*tags:|$)`)
	tagsTail   = regexp.MustCompile(`·\s*tags:\s*([a-z0-9, _-]+)\s*$`)
)

// decorate pulls the structured fields out of a task's assembled text, leaving Text as written.
func decorate(t *Task) {
	// Priority is the first **bold** token, when it is one of the known labels. The old edition
	// grammar's suffix (**P2/EE**) is read as a tag, so that history imports losslessly.
	if m := boldTag.FindStringSubmatch(t.Text); m != nil {
		tok := strings.TrimSpace(m[1])
		if base, suffix, ok := splitEdition(tok); ok && priTag.MatchString(base) {
			t.Priority = base
			t.Rank = rankOf(base)
			if len(suffix) > 0 {
				t.Tags = append(t.Tags, strings.ToLower(suffix))
			}
		}
	}
	// The trailing "— slug", read from the first line so a slug inside prose does not win.
	head := t.Text
	if i := strings.IndexByte(head, '\n'); i >= 0 {
		head = head[:i]
	}
	if m := emDashTail.FindStringSubmatch(head); m != nil {
		t.Slug = m[1]
	}
	if m := parenTail.FindStringSubmatch(head); m != nil {
		t.Touch = splitList(m[1])
	}
	if m := depTail.FindStringSubmatch(head); m != nil {
		t.DepText = strings.TrimSpace(m[1])
	}
	if m := tagsTail.FindStringSubmatch(head); m != nil {
		t.Tags = append(t.Tags, splitList(strings.ToLower(m[1]))...)
	}
	t.Tags = normTags(t.Tags)
	// The DONE annotation, if any — its sha and the note after it.
	if m := doneTag.FindStringSubmatchIndex(t.Text); m != nil {
		if m[2] >= 0 {
			t.DoneSHA = t.Text[m[2]:m[3]]
		}
		t.DoneNote = strings.TrimSpace(t.Text[m[1]:])
		if t.Status == StatusOpen {
			t.Status = StatusDone // a DONE annotation on an unchecked box is still done
		}
	}
	// With the fields extracted, Text becomes the task's BODY: the prose without the metadata that
	// now lives in columns. render rebuilds the line from the pieces, so keeping them in the text
	// too would be one fact in two places, and an edit to a field would not reach the rendered line.
	t.Text = stripMeta(t.Text)
}

var (
	leadPri    = regexp.MustCompile(`^\*\*(P[0-5]|E[0-9]|v2|EE)(/EE)?\*\*\s*`)
	tailTags   = regexp.MustCompile(`\s*·\s*tags:.*$`)
	tailDep    = regexp.MustCompile(`\s*·\s*dep:.*$`)
	tailSlug   = regexp.MustCompile(`\s*—\s*[a-z0-9]+(?:-[a-z0-9]+)*(?:,\s*[a-z0-9]+(?:-[a-z0-9]+)*)*\s*$`)
	tailParen  = regexp.MustCompile(`\s*\([^()]*\)\s*$`)
	tailDoneNo = regexp.MustCompile(`\s*\*\*DONE.*$`)
)

// stripMeta removes from a task's text the metadata that is now held in fields — the leading
// priority tag and the trailing "(touchpoints) — slug · dep: …" run — so what remains is the task
// itself. It works on the FIRST line only (that is where the metadata lives) and leaves any further
// prose lines untouched. The order matters: dep before slug before touchpoints, because each sits
// to the right of the next and stripping the outer one first exposes the next.
func stripMeta(text string) string {
	head, rest, multi := strings.Cut(text, "\n")
	head = tailDoneNo.ReplaceAllString(head, "")
	head = tailTags.ReplaceAllString(head, "")
	head = tailDep.ReplaceAllString(head, "")
	head = tailSlug.ReplaceAllString(head, "")
	head = tailParen.ReplaceAllString(head, "")
	head = leadPri.ReplaceAllString(head, "")
	head = strings.TrimSpace(head)
	if multi {
		return head + "\n" + rest
	}
	return head
}

// splitEdition separates a "P2/EE" tag into "P2" and "EE".
func splitEdition(tok string) (base, ed string, ok bool) {
	if before, after, ok0 := strings.Cut(tok, "/"); ok0 {
		return before, after, true
	}
	return tok, "", true
}

func stripCheckbox(line string) string {
	return taskStart.ReplaceAllString(line, "")
}

// epicName is the epic as an identity: the heading up to the first " — ", which is where these
// headings put the prose gloss.
func epicName(h string) string {
	if i := strings.Index(h, " — "); i >= 0 {
		h = h[:i]
	}
	return strings.TrimSpace(strings.Trim(h, "#"))
}

// slugFor turns an epic name into the middle of a task id: a couple of alphanumeric words, kebab.
func slugFor(epic string) string {
	var b strings.Builder
	words := 0
	for w := range strings.FieldsSeq(strings.ToLower(epic)) {
		clean := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, w)
		if len(clean) == 0 {
			continue
		}
		if words > 0 {
			b.WriteByte('-')
		}
		b.WriteString(clean)
		if words++; words == 2 {
			break
		}
	}
	if b.Len() == 0 {
		return "misc"
	}
	return b.String()
}

func splitList(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); len(p) > 0 {
			out = append(out, p)
		}
	}
	return out
}
