package sqspipeline

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
)

// InstallTracingHooks wires the trace-ID extractor so log lines produced
// outside HTTP handlers (SQS consumers, background goroutines) carry the
// active trace_id via port.SlogStyleLogger.*Context calls.
//
// The span-context extractor and the consumer-span factory are OTel-specific
// and are installed separately by the composition root (cmd/server/hooks.go)
// after this call, keeping direct OTel imports out of internal adapter
// packages per the platform observability standard.
//
// Must be called after gincommon.InitTracingWithConfig.
func InstallTracingHooks(_ string) {
	port.SetTraceExtractor(gincommon.SpanTraceID)
}
