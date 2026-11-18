// Package http provides the inbound HTTP adapter. All routing and middleware
// wiring lives here — cmd/server/main.go constructs dependencies and passes
// them in via RouterConfig.
package http

import (
	"context"
	"net/http"

	apispec "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/api"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/gin-gonic/gin"
)

// Pinger is the health-check interface for dependencies (Postgres, Valkey, outbox, consumers).
type Pinger interface {
	Health(ctx context.Context) error
}

// DocsConfig controls Swagger UI / AsyncAPI access.
type DocsConfig struct {
	Environment string
	Enabled     bool
	AuthToken   string
}

// RouterConfig carries all dependencies the router needs.
type RouterConfig struct {
	GinConfig gincommon.Config
	Docs      DocsConfig

	EntitlementHandler *EntitlementHandler
	OverrideHandler    *OverrideHandler
	AddonHandler       *AddonHandler
	ShortfallHandler   *ShortfallHandler
	TrialHandler       *TrialHandler
	PlansHandler       *PlansHandler

	Postgres  Pinger
	Cache     Pinger
	Outbox    Pinger
	Liveness  []Pinger
	Consumers []Pinger
}

// Router wraps a gin.Engine and exposes the http.Handler.
type Router struct {
	engine *gin.Engine
}

// Handler returns the http.Handler for use with http.Server.
func (r *Router) Handler() http.Handler {
	return r.engine
}

// NewRouter wires up all routes with gincommon middleware:
//   - ObservabilityMiddlewares (Prometheus, OTel tracing, request logging)
//   - ProtectedMiddlewares (JWT auth, x-tenant-id gate)
//
// Route groups:
//   - GET  /api/v1/internal/tenants/:id/entitlements  (ENT-I1)
//   - GET  /api/v1/tenants/:id/entitlements            (ENT-T1 — tenant-scoped JWT)
//   - /api/v1/operator/**                              (operator endpoints)
func NewRouter(cfg RouterConfig) *Router {
	engine := gin.New()

	// ObservabilityMiddlewares installs Prometheus + OTel spans + access log +
	// request timeout. Must be first so all other handlers are traced.
	obsMiddlewares := gincommon.ObservabilityMiddlewares(cfg.GinConfig)
	engine.Use(obsMiddlewares...)

	// ── Health / readiness ───────────────────────────────────────────────
	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	engine.GET("/readyz", func(c *gin.Context) {
		ctx := c.Request.Context()
		checks := map[string]Pinger{
			"postgres": cfg.Postgres,
			"cache":    cfg.Cache,
			"outbox":   cfg.Outbox,
		}
		for name, p := range checks {
			if p == nil {
				continue
			}
			if err := p.Health(ctx); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "dependency": name, "error": err.Error()})
				return
			}
		}
		for i, p := range cfg.Consumers {
			if err := p.Health(ctx); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "consumer": i, "error": err.Error()})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	engine.GET("/livez", func(c *gin.Context) {
		ctx := c.Request.Context()
		for i, p := range cfg.Liveness {
			if err := p.Health(ctx); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "check": i, "error": err.Error()})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// ── AsyncAPI spec endpoint ────────────────────────────────────────────
	engine.GET("/asyncapi.yaml", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/yaml; charset=utf-8", apispec.AsyncAPISpec)
	})

	// ── Docs (Swagger + AsyncAPI UI) ─────────────────────────────────────
	if cfg.Docs.Enabled {
		registerDocsRoutes(engine, cfg.Docs)
	}

	// ── API v1 routes ─────────────────────────────────────────────────────
	api := engine.Group("/api/v1")

	// Internal service-to-service routes (ENT-I1, ENT-I2, ENT-I3).
	// protected by internalServiceRequired() middleware (x-service-token).
	internal := api.Group("/internal")
	internal.Use(internalServiceRequired())
	{
		internalTenant := internal.Group("/tenants/:id")
		internalTenant.Use(tenantIDGate())
		internalTenant.GET("/entitlements", cfg.EntitlementHandler.GetEffectiveSetInternal)       // ENT-I1
		internalTenant.POST("/entitlements/recompute", cfg.EntitlementHandler.Recompute)          // ENT-I1b
		internalTenant.GET("/plans/current", cfg.PlansHandler.GetCurrentPlan)                     // ENT-I2
		internalTenant.POST("/entitlements/drawdown", cfg.AddonHandler.RecordDrawdownInternal)    // ENT-I3
	}

	// Tenant-scoped JWT routes (ENT-T1).
	tenantRoutes := api.Group("/tenants/:id")
	tenantRoutes.Use(gincommon.ProtectedMiddlewares(cfg.GinConfig)...)
	tenantRoutes.Use(tenantIDGate())
	{
		tenantRoutes.GET("/entitlements", cfg.EntitlementHandler.GetEffectiveSetTenant) // ENT-T1
	}

	// Operator admin routes.
	operator := api.Group("/operator")
	operator.Use(gincommon.ProtectedMiddlewares(cfg.GinConfig)...)
	operator.Use(operatorRequired())
	{
		opTenant := operator.Group("/tenants/:id")
		opTenant.Use(tenantIDGate())

		// Overrides (ENT-O1, ENT-O2).
		opOverrides := opTenant.Group("/entitlements/overrides")
		opOverrides.GET("", cfg.OverrideHandler.List)                      // ENT-O1
		opOverrides.GET("/:limitKey", cfg.OverrideHandler.Get)
		opOverrides.PUT("/:limitKey", cfg.OverrideHandler.Set)             // ENT-O2 set
		opOverrides.DELETE("/:limitKey", cfg.OverrideHandler.Remove)       // ENT-O2 remove
		opOverrides.GET("/:limitKey/history", cfg.OverrideHandler.History)

		// Trial (ENT-O3, ENT-O4).
		opTenant.PUT("/entitlements/trial-profile", cfg.TrialHandler.AssignProfile)         // ENT-O3
		opTenant.PUT("/entitlements/trial-overrides/:limitKey", cfg.TrialHandler.SetOverride) // ENT-O4
		opTenant.DELETE("/entitlements/trial-overrides/:limitKey", cfg.TrialHandler.RemoveOverride)

		// Addons (ENT-O5).
		opAddons := opTenant.Group("/entitlements/addons")
		opAddons.GET("", cfg.AddonHandler.List)                       // ENT-O5 list
		opAddons.POST("", cfg.AddonHandler.Add)                       // ENT-O5 add
		opAddons.DELETE("/:addonId", cfg.AddonHandler.Remove)         // ENT-O5 remove
		opAddons.GET("/drawdowns", cfg.AddonHandler.ListDrawdowns)

		// Shortfalls (ENT-O6, ENT-O7).
		opAddons.GET("/shortfalls", cfg.ShortfallHandler.List)                              // ENT-O6
		opAddons.POST("/shortfalls/:shortfallId/resolve", cfg.ShortfallHandler.Resolve)    // ENT-O7
	}

	return &Router{engine: engine}
}

// registerDocsRoutes adds Swagger UI and AsyncAPI Studio endpoints.
func registerDocsRoutes(engine *gin.Engine, docs DocsConfig) {
	// AsyncAPI Studio and Swagger UI are served conditionally when DOCS_ENABLED=true.
	engine.GET("/asyncapi", func(c *gin.Context) {
		if docs.AuthToken != "" && c.GetHeader("Authorization") != "Bearer "+docs.AuthToken {
			c.Status(http.StatusUnauthorized)
			return
		}
		// Serve a minimal AsyncAPI Studio HTML page embedding the spec URL.
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, asyncAPIStudioHTML)
	})
}

const asyncAPIStudioHTML = `<!DOCTYPE html><html><head><title>AsyncAPI Studio – Commercial Entitlements</title></head>
<body><asyncapi-component schemaUrl="/asyncapi.yaml"></asyncapi-component>
<script src="https://unpkg.com/@asyncapi/react-component@latest/browser/standalone/index.js"></script></body></html>`
