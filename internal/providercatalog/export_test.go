// Test seams: helpers only test code uses, kept out of the production binary.
package providercatalog

func IDs() []string {
	ids := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		ids = append(ids, descriptor.ID)
	}
	return ids
}
