// Package http provides the inbound HTTP adapter. All routing and middleware
// wiring lives here — cmd/server/main.go constructs dependencies and passes
// them in via RouterConfig.
package http

import (
	"context"
	"net/http"
	"strings"

	apispec "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/api"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
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

	// BillingPrincipal is Billing's mTLS principal
	// ("cluster.local/ns/billing/sa/commercial-billing"). A request carrying it
	// in X-Forwarded-Client-Cert is admitted to the internal group and confined
	// to GET ENT-I1 (B-2). Empty disables principal admission.
	BillingPrincipal string

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
	// internalServiceRequired admits x-service-token callers and Billing by its
	// mTLS principal; billingReadOnly confines Billing to GET ENT-I1.
	internal := api.Group("/internal")
	internal.Use(internalServiceRequired(cfg.BillingPrincipal))
	internal.Use(billingReadOnly(cfg.BillingPrincipal))
	{
		internalTenant := internal.Group("/tenants/:id")
		internalTenant.Use(tenantIDGate())
		internalTenant.GET("/entitlements", cfg.EntitlementHandler.GetEffectiveSetInternal)    // ENT-I1
		internalTenant.POST("/entitlements/recompute", cfg.EntitlementHandler.Recompute)       // ENT-I1b
		internalTenant.GET("/plans/current", cfg.PlansHandler.GetCurrentPlan)                  // ENT-I2
		internalTenant.POST("/entitlements/drawdown", cfg.AddonHandler.RecordDrawdownInternal) // ENT-I3
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
		opOverrides.GET("", cfg.OverrideHandler.List) // ENT-O1
		opOverrides.GET("/:limitKey", cfg.OverrideHandler.Get)
		opOverrides.PUT("/:limitKey", cfg.OverrideHandler.Set)       // ENT-O2 set
		opOverrides.DELETE("/:limitKey", cfg.OverrideHandler.Remove) // ENT-O2 remove
		opOverrides.GET("/:limitKey/history", cfg.OverrideHandler.History)

		// Trial (ENT-O3, ENT-O4).
		opTenant.PUT("/entitlements/trial-profile", cfg.TrialHandler.AssignProfile) // ENT-O3
		opTenant.GET("/entitlements/trial-profile", cfg.TrialHandler.GetAssignment)
		opTenant.DELETE("/entitlements/trial-profile", cfg.TrialHandler.PurgeTrial)
		opTenant.GET("/entitlements/trial-overrides", cfg.TrialHandler.ListOverrides)         // ENT-O4 list
		opTenant.PUT("/entitlements/trial-overrides/:limitKey", cfg.TrialHandler.SetOverride) // ENT-O4 set
		opTenant.DELETE("/entitlements/trial-overrides/:limitKey", cfg.TrialHandler.RemoveOverride)

		// Addons (ENT-O5).
		opAddons := opTenant.Group("/entitlements/addons")
		opAddons.GET("", cfg.AddonHandler.List)               // ENT-O5 list
		opAddons.POST("", cfg.AddonHandler.Add)               // ENT-O5 add
		opAddons.DELETE("/:addonId", cfg.AddonHandler.Remove) // ENT-O5 remove
		opAddons.GET("/drawdowns", cfg.AddonHandler.ListDrawdowns)

		// Shortfalls (ENT-O6, ENT-O7).
		opAddons.GET("/shortfalls", cfg.ShortfallHandler.List)                          // ENT-O6
		opAddons.POST("/shortfalls/:shortfallId/resolve", cfg.ShortfallHandler.Resolve) // ENT-O7

		// Plan catalog (operator visibility).
		opPlans := operator.Group("/plans")
		opPlans.GET("", cfg.PlansHandler.ListPlans)
		opPlans.POST("/:planCode/refresh", cfg.PlansHandler.RefreshCatalog)
	}

	return &Router{engine: engine}
}

// registerDocsRoutes adds Swagger UI (with BCBP dark theme) and AsyncAPI Studio endpoints.
func registerDocsRoutes(engine *gin.Engine, docs DocsConfig) {
	// Swagger UI — /swagger/*any.
	// The docs/swagger package is blank-imported in cmd/server/main.go so its
	// init() registers the spec with the swag runtime before this runs.
	// swagger-theme.css and swagger-initializer.js are intercepted here so
	// the BCBP dark theme and definition-order sorting are applied.
	stdSwagger := ginSwagger.WrapHandler(swaggerFiles.Handler)
	engine.GET("/swagger/*any", func(c *gin.Context) {
		if docs.AuthToken != "" && c.GetHeader("Authorization") != "Bearer "+docs.AuthToken {
			c.Status(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(c.Request.URL.Path, "/swagger-theme.css"):
			SwaggerThemeHandler(c)
		case strings.HasSuffix(c.Request.URL.Path, "/swagger-initializer.js"):
			SwaggerInitializerHandler(c)
		default:
			stdSwagger(c)
		}
	})

	// AsyncAPI event-contract viewer — /asyncapi.
	// No CDN dependency: spec is parsed from api/asyncapi.yaml (embedded via
	// api/embed.go) and rendered as a fully self-contained HTML page by
	// AsyncAPIHandler (internal/adapter/inbound/http/asyncapi.go).
	engine.GET("/asyncapi", func(c *gin.Context) {
		if docs.AuthToken != "" && c.GetHeader("Authorization") != "Bearer "+docs.AuthToken {
			c.Status(http.StatusUnauthorized)
			return
		}
		AsyncAPIHandler(c)
	})
}
