package tui

import "github.com/charmbracelet/bubbles/key"

type KeyMap struct {
	Up       key.Binding
	Down     key.Binding
	Open     key.Binding
	OpenWith key.Binding
	New      key.Binding
	Delete   key.Binding
	Toggle   key.Binding
	Collapse key.Binding
	Expand   key.Binding
	Refresh  key.Binding
	AddRepo  key.Binding
	Help     key.Binding
	Quit     key.Binding
}

var DefaultKeyMap = KeyMap{
	Up:       key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
	Down:     key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
	Open:     key.NewBinding(key.WithKeys("enter", "o"), key.WithHelp("enter/o", "open")),
	OpenWith: key.NewBinding(key.WithKeys("O"), key.WithHelp("O", "open with model")),
	New:      key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new worktree")),
	Delete:   key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
	Toggle:   key.NewBinding(key.WithKeys(" ", "tab"), key.WithHelp("space", "fold/unfold")),
	Collapse: key.NewBinding(key.WithKeys("h", "left"), key.WithHelp("h/←", "collapse")),
	Expand:   key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l/→", "expand")),
	Refresh:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	AddRepo:  key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "add repo")),
	Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit:     key.NewBinding(key.WithKeys("q", "esc"), key.WithHelp("q", "quit")),
}
