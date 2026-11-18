package domain

// producedEvents lists all event types this service emits (produces) to SNS.
// Used by the GlueCodec to determine which schema names to register.
var producedEvents = map[string]bool{
	EventTypeTenantEntitlementsChanged: true,
}

// IsProducedEvent reports whether the service produces events of this type.
func IsProducedEvent(eventType string) bool {
	return producedEvents[eventType]
}
