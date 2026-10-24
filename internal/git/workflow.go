package git

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// workflowJob is what kijk reads of a job in a GitHub workflow file: its id, its name, and the
// jobs it needs.
type workflowJob struct {
	ID    string
	Name  string
	Needs []string
}

var errWorkflow = errors.New("workflow not read")

type yamlLine struct {
	indent int
	text   string
}

// parseWorkflow reads jobs.<id>.name and jobs.<id>.needs, and nothing else. It is no YAML parser:
// any form it does not know is an error, so the caller shows a list instead of a wrong graph.
func parseWorkflow(src string) ([]workflowJob, error) {
	var ls []yamlLine
	for _, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		t := stripComment(raw)
		if strings.TrimSpace(t) == "" {
			continue
		}
		trimmed := strings.TrimLeft(t, " ")
		ls = append(ls, yamlLine{len(t) - len(trimmed), strings.TrimRight(trimmed, " \t")})
	}
	start := slices.IndexFunc(ls, func(l yamlLine) bool { return l.indent == 0 && strings.HasPrefix(l.text, "jobs:") })
	if start < 0 || strings.TrimSpace(strings.TrimPrefix(ls[start].text, "jobs:")) != "" {
		return nil, errWorkflow
	}
	var jobs []workflowJob
	jobIndent, propIndent := -1, -1
	for i := start + 1; i < len(ls); i++ {
		l := ls[i]
		if l.indent == 0 {
			break
		}
		if strings.ContainsAny(l.text, "&*") && anchorRe.MatchString(l.text) {
			return nil, errWorkflow // an anchor or alias may carry needs kijk cannot see
		}
		if jobIndent < 0 {
			jobIndent = l.indent
		}
		switch {
		case l.indent < jobIndent:
			return nil, errWorkflow
		case l.indent == jobIndent:
			k, v, ok := keyValue(l.text)
			if !ok || v != "" {
				return nil, errWorkflow
			}
			jobs = append(jobs, workflowJob{ID: k})
			propIndent = -1
		default:
			if propIndent < 0 {
				propIndent = l.indent
			}
			if l.indent != propIndent {
				continue // inside a property such as steps
			}
			k, v, ok := keyValue(l.text)
			if !ok {
				continue
			}
			j := &jobs[len(jobs)-1]
			switch k {
			case "name":
				j.Name = unquote(v)
			case "needs":
				var err error
				if j.Needs, err = readNeeds(v, ls[i+1:], propIndent); err != nil {
					return nil, err
				}
			}
		}
	}
	if len(jobs) == 0 {
		return nil, errWorkflow
	}
	ids := map[string]bool{}
	for _, j := range jobs {
		ids[j.ID] = true
	}
	for _, j := range jobs {
		for _, n := range j.Needs {
			if !ids[n] {
				return nil, errWorkflow
			}
		}
	}
	return jobs, nil
}

var anchorRe = regexp.MustCompile(`(^|[\s:\[,-])[&*][A-Za-z0-9_-]`)

// readNeeds reads a needs value: a job id, a flow list on one line, or a block list on the lines
// after it, which follow as rest.
func readNeeds(v string, rest []yamlLine, indent int) ([]string, error) {
	switch {
	case v == "":
		var out []string
		for _, l := range rest {
			if l.indent <= indent {
				break
			}
			item, ok := strings.CutPrefix(l.text, "- ")
			if !ok {
				return nil, errWorkflow
			}
			out = append(out, unquote(strings.TrimSpace(item)))
		}
		if len(out) == 0 {
			return nil, errWorkflow
		}
		return out, nil
	case strings.HasPrefix(v, "["):
		if !strings.HasSuffix(v, "]") {
			return nil, errWorkflow
		}
		var out []string
		for _, p := range strings.Split(v[1:len(v)-1], ",") {
			if p = unquote(strings.TrimSpace(p)); p != "" {
				out = append(out, p)
			}
		}
		return out, nil
	case strings.ContainsAny(v, "{}$|>"):
		return nil, errWorkflow
	}
	return []string{unquote(v)}, nil
}

var keyRe = regexp.MustCompile(`^("[^"]*"|'[^']*'|[A-Za-z0-9_.-]+):(\s+(.*))?$`)

func keyValue(s string) (k, v string, ok bool) {
	m := keyRe.FindStringSubmatch(s)
	if m == nil {
		return "", "", false
	}
	return unquote(m[1]), strings.TrimSpace(m[3]), true
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// stripComment cuts a # comment that starts the line or follows a space, outside quotes.
func stripComment(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return s[:i]
		}
	}
	return s
}

var matrixRe = regexp.MustCompile(` \([^()]*\)$`)

// matchJob finds the job of a check run by its name: the job's name, else its id, also with the
// matrix part "(linux)" cut off, and for a reusable workflow ("caller / callee") the caller.
func matchJob(api string, jobs []workflowJob) (string, bool) {
	first, _, _ := strings.Cut(api, " / ")
	for _, c := range []string{api, matrixRe.ReplaceAllString(api, ""), first, matrixRe.ReplaceAllString(first, "")} {
		for _, j := range jobs {
			if (j.Name != "" && j.Name == c) || (j.Name == "" && j.ID == c) {
				return j.ID, true
			}
		}
	}
	return "", false
}
