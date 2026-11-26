// Package main global Swagger annotations. `swag init` reads this file
// (via `-g swagger_info.go`) to build the top-level OpenAPI/Swagger
// specification — title, version, host, base path, security schemes,
// and tag descriptions with display order. Per-handler `// @…` annotations
// live next to each handler under internal/adapter/inbound/http/.
//
// Regenerate the spec with:
//
//	make swag
//
// The generated files under docs/swagger/ are checked in; CI's make
// swag-check fails a PR whose annotations diverge from them.
//
// @title          Commercial Entitlements Service
// @version        1.0
// @description    Manages per-tenant entitlement sets for the XpertPMS commercial domain.
// @description
// @description    **Capabilities.** Plan-based limits · manual operator overrides · quota addon packs · trial profile assignment · drawdown recording · shortfall raise/resolve · plan catalog read-through.
// @description
// @description    **Route prefixes.** `/api/v1/internal/*` — in-mesh services (mTLS, no JWT). `/api/v1/tenants/*` — tenant-scoped JWT callers. `/api/v1/operator/*` — platform operators.
// @description
// @description    **Tenant isolation.** Every resource is scoped by `x-tenant-id`; cross-tenant reads are blocked by row-level security (RLS-1..RLS-6).
// @description
// @description    Every mutation echoes `record_version` for optimistic-locking (CONC-1).
//
// @contact.name   BCBP Solutions Platform Team
// @contact.email  sharmila.dayalan@bcbpsolutions.com
//
// @license.name   Proprietary
//
// @host      localhost:8080
// @BasePath  /
//
// @securityDefinitions.apikey TenantID
// @in                         header
// @name                       x-tenant-id
// @description                Tenant UUID injected by the API gateway. Establishes the RLS scope (RLS-6).
//
// @securityDefinitions.apikey UserID
// @in                         header
// @name                       x-user-id
// @description                Authenticated user UUID. Use `iam-system` for in-mesh service calls on `/api/v1/internal/*`.
//
// @securityDefinitions.apikey TenantRoles
// @in                         header
// @name                       x-tenant-roles
// @description                Comma-separated role list. Operator routes require `platform_operator`; internal routes require `iam-system`.
//
// Tag order defines the display order in Swagger UI (tagsSorter: identity function in swagger_initializer.js).
//
// @tag.name         internal
// @tag.description  In-mesh service-to-service calls (ENT-I1..ENT-I3)
//
// @tag.name         entitlements
// @tag.description  Effective entitlement set — read and recompute (ENT-I1, ENT-T1)
//
// @tag.name         overrides
// @tag.description  Operator manual limit overrides (ENT-O1, ENT-O2)
//
// @tag.name         addons
// @tag.description  Quota addon packs and drawdown recording (ENT-O5, ENT-I3)
//
// @tag.name         shortfalls
// @tag.description  Quota shortfall raise and resolution (ENT-O6, ENT-O7)
//
// @tag.name         trial
// @tag.description  Trial profile assignment and trial-scoped overrides (ENT-O3, ENT-O4)
//
// @tag.name         plans
// @tag.description  Plan catalog read-through and cache refresh
package main
