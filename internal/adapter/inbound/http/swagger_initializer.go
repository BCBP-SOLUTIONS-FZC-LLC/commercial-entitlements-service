package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SwaggerInitializerHandler serves swagger-initializer.js with the BCBP
// configuration: dark theme, definition-order tag/operation sorting, and a
// plugin that keeps model collapse toggles visible after expand.
func SwaggerInitializerHandler(c *gin.Context) {
	c.Header("Content-Type", "application/javascript; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.String(http.StatusOK, swaggerInitializerJS)
}

const swaggerInitializerJS = `
// Inject BCBP dark theme CSS.
(function() {
  var link = document.createElement('link');
  link.rel = 'stylesheet'; link.type = 'text/css';
  link.href = window.location.pathname.replace(/\/[^/]*$/, '/swagger-theme.css');
  document.head.appendChild(link);
})();

window.onload = function() {
  const KeepModelTogglePlugin = function() {
    return {
      wrapComponents: {
        ModelCollapse: function(Original, system) {
          return function(props) {
            var next = Object.assign({}, props);
            if (props.collapsedContent === '[...]') { next.hideSelfOnExpand = false; }
            return system.React.createElement(Original, next);
          };
        }
      }
    };
  };

  const ui = SwaggerUIBundle({
    url: "doc.json",
    dom_id: '#swagger-ui',
    validatorUrl: null,
    oauth2RedirectUrl: ` + "`${window.location.protocol}//${window.location.host}${window.location.pathname.split('/').slice(0, window.location.pathname.split('/').length - 1).join('/')}/oauth2-redirect.html`" + `,
    persistAuthorization: false,
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    plugins: [SwaggerUIBundle.plugins.DownloadUrl, KeepModelTogglePlugin],
    layout: "StandaloneLayout",
    docExpansion: "list",
    deepLinking: true,
    defaultModelsExpandDepth: 1,
    // Preserve tag and operation order from swagger_info.go (internal →
    // entitlements → overrides → addons → shortfalls → trial → plans).
    // The identity function (return 0) keeps the definition order verbatim.
    tagsSorter: function() { return 0; },
    operationsSorter: function() { return 0; }
  });

  window.ui = ui;
};
`
