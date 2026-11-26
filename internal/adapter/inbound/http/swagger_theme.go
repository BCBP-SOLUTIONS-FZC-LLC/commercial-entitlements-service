package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SwaggerThemeHandler serves the BCBP-branded CSS overrides for Swagger UI.
func SwaggerThemeHandler(c *gin.Context) {
	c.Header("Content-Type", "text/css; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.String(http.StatusOK, swaggerThemeCSS)
}

const swaggerThemeCSS = `
@import url('https://fonts.googleapis.com/css2?family=Poppins:wght@300;400;500;600;700&display=swap');

/* ── Base ─────────────────────────────────────────────────────────────────── */
body, .swagger-ui { background: #000 !important; font-family: "Poppins", sans-serif !important; }
.swagger-ui * { box-sizing: border-box; }

/* ── Topbar ───────────────────────────────────────────────────────────────── */
.swagger-ui .topbar {
  background: #000 !important;
  border-bottom: 2px solid;
  border-image: linear-gradient(to right, #8426b0 3%, #bd0283 47%, #ec4b3c 98%) 1;
  padding: .75rem 0;
}
.swagger-ui .topbar a, .swagger-ui .topbar .topbar-wrapper span { color: #fff !important; }
.swagger-ui .topbar .download-url-wrapper input[type=text] {
  background: #0d0d0d !important; border-color: rgba(132,38,176,.4) !important; color: #fff !important;
}
.swagger-ui .topbar .download-url-wrapper .download-url-button {
  background: linear-gradient(to right, #8426b0, #bd0283) !important; border: none !important; color: #fff !important;
}

/* ── Info section ─────────────────────────────────────────────────────────── */
.swagger-ui .information-container {
  background: #0d0d0d !important;
  border-bottom: 2px solid !important;
  border-image: linear-gradient(to right,#8426b0 3%,#bd0283 47%,#ec4b3c 98%) 1 !important;
  padding: 1.5rem 0 !important;
}
.swagger-ui .info .title {
  font-family: "Poppins", sans-serif !important; font-weight: 700 !important;
  background: linear-gradient(to right,#8426b0 3%,#bd0283 47%,#ec4b3c 98%) !important;
  -webkit-background-clip: text !important; -webkit-text-fill-color: transparent !important; background-clip: text !important;
}
.swagger-ui .info .base-url, .swagger-ui .base-url {
  background: #000 !important; border: 1px solid rgba(132,38,176,.4) !important; border-radius: 6px !important;
  color: #e879f9 !important; -webkit-text-fill-color: #e879f9 !important; display: inline-block !important;
  font-family: monospace !important; font-size: .85rem !important; padding: 3px 10px !important; margin: .5rem 0 !important;
}
.swagger-ui .info__contact a {
  background: linear-gradient(to right,#8426b0,#bd0283); -webkit-background-clip: text !important;
  -webkit-text-fill-color: transparent !important; background-clip: text !important; font-weight: 600 !important;
}
.swagger-ui .info p, .swagger-ui .info li, .swagger-ui .info h1, .swagger-ui .info h3 { color: #d1d5db !important; }
.swagger-ui .info a { color: #bd0283 !important; }
.swagger-ui .info code { background: #0d0d0d !important; color: #e879f9 !important; }

/* ── Scheme bar ───────────────────────────────────────────────────────────── */
.swagger-ui .scheme-container {
  background: #050505 !important; box-shadow: none !important;
  border-bottom: 1px solid rgba(132,38,176,.25) !important;
}
.swagger-ui .schemes > label { color: #9ca3af !important; }

/* ── Operation tags ───────────────────────────────────────────────────────── */
.swagger-ui .opblock-tag {
  color: #fff !important; border-bottom: 1px solid rgba(132,38,176,.25) !important;
  font-family: "Poppins", sans-serif !important; font-weight: 600 !important;
}
.swagger-ui .opblock-tag:hover { background: rgba(132,38,176,.08) !important; }

/* ── Operation blocks ─────────────────────────────────────────────────────── */
.swagger-ui .opblock {
  background: #0d0d0d !important; border-color: rgba(132,38,176,.3) !important;
  border-radius: 8px !important; margin-bottom: .5rem !important;
}
.swagger-ui .opblock .opblock-summary { border-color: rgba(132,38,176,.3) !important; }
.swagger-ui .opblock .opblock-summary-path { color: #fff !important; font-family: monospace !important; }
.swagger-ui .opblock .opblock-summary-description { color: #9ca3af !important; }
.swagger-ui .opblock-body { background: #0a0a0a !important; }

/* ── Method colours ───────────────────────────────────────────────────────── */
.swagger-ui .opblock.opblock-get  { border-color: #1e3a5f !important; background: rgba(30,58,95,.08) !important; }
.swagger-ui .opblock.opblock-get  .opblock-summary-method { background: #1e3a5f !important; color: #60a5fa !important; }
.swagger-ui .opblock.opblock-post { border-color: #1a3028 !important; background: rgba(26,48,40,.08) !important; }
.swagger-ui .opblock.opblock-post .opblock-summary-method { background: #1a3028 !important; color: #4ade80 !important; }
.swagger-ui .opblock.opblock-put  { border-color: rgba(132,38,176,.5) !important; background: rgba(132,38,176,.06) !important; }
.swagger-ui .opblock.opblock-put  .opblock-summary-method { background: linear-gradient(to right,#8426b0,#bd0283) !important; color: #fff !important; }
.swagger-ui .opblock.opblock-delete { border-color: #ec4b3c !important; background: rgba(236,75,60,.06) !important; }
.swagger-ui .opblock.opblock-delete .opblock-summary-method { background: #ec4b3c !important; color: #fff !important; }

/* ── Parameters ───────────────────────────────────────────────────────────── */
.swagger-ui .opblock .opblock-section-header {
  background: rgba(132,38,176,.08) !important; border-bottom: 1px solid rgba(132,38,176,.2) !important;
  box-shadow: none !important;
}
.swagger-ui .opblock .opblock-section-header h4 { color: #fff !important; font-family: "Poppins", sans-serif !important; font-weight: 600 !important; }
.swagger-ui .opblock .opblock-section-header .btn.try-out__btn {
  background: transparent !important; border: 1px solid rgba(132,38,176,.45) !important; color: #e879f9 !important;
}
.swagger-ui .opblock .opblock-section-header .btn.try-out__btn:hover { background: rgba(132,38,176,.14) !important; color: #fff !important; }
.swagger-ui .parameters-col_description input[type=text], .swagger-ui .parameters-col_description textarea {
  background: #111 !important; border: 1px solid rgba(132,38,176,.35) !important; color: #fff !important;
}
.swagger-ui .parameters-container .parameter__name { color: #fff !important; font-weight: 600 !important; }
.swagger-ui .parameters-container .parameter__type { color: #e879f9 !important; font-family: monospace !important; }
.swagger-ui .parameters-container .parameter__in { color: #9ca3af !important; font-style: italic !important; }
.swagger-ui table thead tr td, .swagger-ui table thead tr th { color: #9ca3af !important; border-bottom: 1px solid rgba(132,38,176,.25) !important; }
.swagger-ui table tbody tr td { color: #d1d5db !important; border-bottom: 1px solid rgba(132,38,176,.12) !important; }

/* ── Inputs & Buttons ─────────────────────────────────────────────────────── */
.swagger-ui input[type=text], .swagger-ui textarea, .swagger-ui select {
  background: #0d0d0d !important; border: 1px solid rgba(132,38,176,.4) !important; color: #fff !important;
}
.swagger-ui .btn { font-family: "Poppins", sans-serif !important; font-weight: 500 !important; border-radius: 6px !important; }
.swagger-ui .btn.authorize { background: linear-gradient(to right, #8426b0, #bd0283) !important; border-color: transparent !important; color: #fff !important; }
.swagger-ui .btn.execute { background: linear-gradient(to right, #8426b0, #bd0283, #ec4b3c) !important; border-color: transparent !important; color: #fff !important; }
.swagger-ui .btn.cancel { background: #ec4b3c !important; border-color: #ec4b3c !important; color: #fff !important; }

/* ── Responses ────────────────────────────────────────────────────────────── */
.swagger-ui .response-col_status { color: #22c55e !important; font-weight: 700 !important; }
.swagger-ui .microlight, .swagger-ui .highlight-code, .swagger-ui pre:not(.version) { background: #0d0d0d !important; color: #e879f9 !important; }
.swagger-ui pre:not(.version) { border: 1px solid rgba(132,38,176,.25) !important; border-radius: 6px !important; }
.swagger-ui code { background: #0d0d0d !important; color: #e879f9 !important; }

/* ── Models ───────────────────────────────────────────────────────────────── */
.swagger-ui section.models { border: 1px solid rgba(132,38,176,.25) !important; background: #0d0d0d !important; border-radius: 8px !important; }
.swagger-ui section.models h4 { color: #fff !important; }
.swagger-ui .model-container { background: #111 !important; border-radius: 6px !important; margin: .5rem 0 !important; }
.swagger-ui .model { color: #d1d5db !important; }
.swagger-ui .model .property.primitive { color: #e879f9 !important; }
.swagger-ui .model-title { color: #fff !important; font-weight: 600 !important; }
.swagger-ui .prop-format { color: #f59e0b !important; }
.swagger-ui .required { color: #ec4b3c !important; font-weight: 600 !important; }

/* ── Auth modal ───────────────────────────────────────────────────────────── */
.swagger-ui .dialog-ux .modal-ux {
  background: #0d0d0d !important; border: 1px solid rgba(132,38,176,.4) !important;
  border-radius: 8px !important; max-width: 560px !important; width: 92% !important;
}
.swagger-ui .dialog-ux .modal-ux-header { background: #000 !important; border-bottom: 1px solid rgba(132,38,176,.25) !important; }
.swagger-ui .dialog-ux .modal-ux-header h3 { color: #fff !important; font-family: "Poppins", sans-serif !important; font-weight: 600 !important; }
.swagger-ui .dialog-ux .modal-ux-content p { color: #e5e7eb !important; font-family: "Poppins", sans-serif !important; }
.swagger-ui .dialog-ux .modal-ux-content h4 { color: #fff !important; font-family: "Poppins", sans-serif !important; font-weight: 600 !important; }
.swagger-ui .dialog-ux .modal-ux-content label { color: #9ca3af !important; font-family: "Poppins", sans-serif !important; font-size: 13px !important; }
.swagger-ui .dialog-ux .modal-ux-content input[type=text] {
  background: #0d0d0d !important; border: 1px solid rgba(132,38,176,.4) !important;
  border-radius: 6px !important; color: #fff !important; max-width: 340px !important; width: 100% !important;
}

/* ── Misc ─────────────────────────────────────────────────────────────────── */
.swagger-ui p, .swagger-ui h1, .swagger-ui h3, .swagger-ui h4, .swagger-ui h5, .swagger-ui h6 { color: #fff !important; }
.swagger-ui span, .swagger-ui label { color: #d1d5db !important; }
.swagger-ui a { color: #bd0283 !important; }
.swagger-ui a:hover { color: #ec4b3c !important; }
.swagger-ui svg { fill: #d1d5db !important; }
.swagger-ui .expand-operation svg, .swagger-ui .expand-methods svg,
.swagger-ui .models-control svg, .swagger-ui .opblock-summary-control svg { fill: #e879f9 !important; }
.swagger-ui .loading-container .loading::after { border-color: #8426b0 transparent transparent !important; }

/* Version badge */
.swagger-ui .info .title small {
  background: linear-gradient(to right, #8426b0, #bd0283) !important;
  -webkit-background-clip: padding-box !important; background-clip: padding-box !important;
  -webkit-text-fill-color: initial !important; border-radius: 4px !important;
  display: inline-block !important; padding: 2px 8px !important; vertical-align: middle !important;
}
.swagger-ui .info .title small pre {
  background: none !important; border: none !important; color: #fff !important;
  -webkit-text-fill-color: #fff !important; font-family: "Poppins", sans-serif !important;
  font-size: .75rem !important; font-weight: 600 !important; margin: 0 !important; padding: 0 !important;
}
`
