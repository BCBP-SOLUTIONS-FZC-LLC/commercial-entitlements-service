package domain

// producedEvents lists all event types this service emits (produces) to SNS.
// Used by the GlueCodec to determine which schema names to register.
// BL-OQ-20: TenantQuotaAddOnChanged is produced exclusively by Entitlements;
// Billing must not emit any of the reserved reasons for this event type.
var producedEvents = map[string]bool{
	EventTypeTenantEntitlementsChanged: true,
	EventTypeTenantAddOnBalanceChanged: true,
}

// IsProducedEvent reports whether the service produces events of this type.
func IsProducedEvent(eventType string) bool {
	return producedEvents[eventType]
}
