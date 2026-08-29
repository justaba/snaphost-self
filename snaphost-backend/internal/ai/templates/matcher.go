package templates

// Match represents a successful template match.
type Match struct {
	Template *Template
	Vars     map[string]string
}

// FindMatch iterates over the template library and returns the first match.
func FindMatch(signals ProjectSignals) (*Match, bool) {
	for i := range Library {
		tmpl := &Library[i]
		if ok, vars := tmpl.Match(signals); ok {
			return &Match{
				Template: tmpl,
				Vars:     vars,
			}, true
		}
	}
	return nil, false
}
