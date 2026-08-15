package todo

import "strings"

// Render turns a set of tasks back into markdown, grouped by epic in file order. It is the inverse
// of Import, but not its mirror: the database is the source now, so this normalises rather than
// preserves — one spelling of the priority tag, one order of the trailing metadata, the fields in
// their columns reassembled the same way every time. That is the point of a rendered artifact:
// two renders of the same data are byte-identical, so a diff shows a change to the DATA and never a
// change of hand.
//
// A caller usually renders one tag's slice at a time (List with a Tag filter), the way the two
// already are two files.
func Render(tasks []Task) string {
	var b strings.Builder
	epic := ""
	for _, t := range tasks {
		if t.Epic != epic {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("## ")
			b.WriteString(t.Epic)
			b.WriteString("\n\n")
			epic = t.Epic
		}
		b.WriteString(renderLine(t))
		b.WriteByte('\n')
	}
	return b.String()
}

// renderLine is one task: the checkbox, the priority tag, the body, then the metadata that was
// lifted into fields, put back in the order Import expects to read it — so a render can be imported
// again with nothing lost.
func renderLine(t Task) string {
	var b strings.Builder
	if t.Status == StatusDone {
		b.WriteString("- [x] ")
	} else {
		b.WriteString("- [ ] ")
	}
	if len(t.Priority) > 0 {
		b.WriteString("**")
		b.WriteString(t.Priority)
		b.WriteString("** ")
	}

	// The body's first line carries the metadata tail; any further lines follow verbatim.
	head, rest, multi := strings.Cut(t.Text, "\n")
	b.WriteString(head)
	if len(t.Touch) > 0 {
		b.WriteString(" (")
		b.WriteString(strings.Join(t.Touch, ", "))
		b.WriteByte(')')
	}
	if len(t.Slug) > 0 {
		b.WriteString(" — ")
		b.WriteString(t.Slug)
	}
	if len(t.DepText) > 0 {
		b.WriteString(" · dep: ")
		b.WriteString(t.DepText)
	}
	// Tags travel as an explicit tail, which is what import reads back — the old /EE suffix was one
	// project's grammar and is understood on the way IN only.
	if len(t.Tags) > 0 {
		b.WriteString(" · tags: ")
		b.WriteString(strings.Join(t.Tags, ", "))
	}
	// The DONE annotation is rebuilt with its marker, so a rendered done task imports back as done
	// with its sha intact rather than as an open task whose text happens to mention it.
	if t.Status == StatusDone && (len(t.DoneSHA) > 0 || len(t.DoneNote) > 0) {
		b.WriteString(" **DONE")
		if len(t.DoneSHA) > 0 {
			b.WriteByte(' ')
			b.WriteString(t.DoneSHA)
		}
		b.WriteString(":**")
		if len(t.DoneNote) > 0 {
			b.WriteByte(' ')
			b.WriteString(t.DoneNote)
		}
	}
	if multi {
		b.WriteByte('\n')
		b.WriteString(rest)
	}
	return b.String()
}
