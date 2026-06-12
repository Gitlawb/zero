package mcp

func ServerNames(servers map[string]string) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	return names
}
