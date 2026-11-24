package domain

// producedEvents lists all event types this service emits (produces) to SNS.
// Used by the GlueCodec to determine which schema names to register.
// Entitlements produces tenant_entitlements_changed on every recompute.
// Billing's TenantQuotaAddOnChanged (purchase/refund) is CONSUMED, not produced here.
var producedEvents = map[string]bool{
	EventTypeTenantEntitlementsChanged: true,
}

// IsProducedEvent reports whether the service produces events of this type.
func IsProducedEvent(eventType string) bool {
	return producedEvents[eventType]
}
