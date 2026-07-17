// Test seams: helpers only test code uses, kept out of the production binary.
package streamjson

func ParsePrompt(input string) (string, error) {
	events, err := ParseInput(input)
	if err != nil {
		return "", err
	}
	return ResolvePrompt(events)
}
