package verify

type Event struct {
	Type string
	Name string
}

func Events() []Event {
	return []Event{
		{Type: "check", Name: "config"},
		{Type: "summary", Name: "verify"},
	}
}
