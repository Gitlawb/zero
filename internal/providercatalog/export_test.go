// Test seams: helpers only test code uses, kept out of the production binary.
package providercatalog

func IDs() []string {
	ids := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		ids = append(ids, descriptor.ID)
	}
	return ids
}

func ListByTransport(transport Transport) []Descriptor {
	normalized := Transport(NormalizeID(string(transport)))
	items := make([]Descriptor, 0)
	for _, descriptor := range descriptors {
		if descriptor.Transport == normalized {
			items = append(items, cloneDescriptor(descriptor))
		}
	}
	return items
}

func ValidAPIFormat(format APIFormat) bool {
	switch format {
	case APIFormatOpenAIResponses, APIFormatOpenAIChatCompletions, APIFormatAnthropicMessages, APIFormatGoogleGenerateContent, APIFormatBedrockConverse, APIFormatVertexGenerateContent:
		return true
	default:
		return false
	}
}

func ValidTransport(transport Transport) bool {
	switch Transport(NormalizeID(string(transport))) {
	case TransportOpenAI, TransportAnthropic, TransportGoogle, TransportBedrock, TransportVertex, TransportOpenAICompatible, TransportAnthropicCompatible:
		return true
	default:
		return false
	}
}
