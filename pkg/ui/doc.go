// Package ui provides the server-rendered web interface: html/template definitions supplied
// at construction as an io/fs.FS (typically embedded from web/templates), static assets as
// an io/fs.FS (typically embedded from web/static), in-memory state for demo content, and
// HTMX-aware handlers that return either full HTML documents (shell + panel) or panel
// fragments when the HX-Request header indicates an in-page swap.
//
// Health reporting (App.Health) validates that required templates exist and that app.css
// is present in the static filesystem so readiness probes reflect real UI dependencies.
package ui
