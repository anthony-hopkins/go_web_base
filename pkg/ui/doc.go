// Package ui provides the server-rendered web interface: html/template definitions under
// web/templates, static CSS under web/static, in-memory state for demo content, and
// HTMX-aware handlers that return either full HTML documents (shell + panel) or panel
// fragments when the HX-Request header indicates an in-page swap.
//
// Health reporting (App.Health) validates that required templates exist and that
// web/static/app.css is readable so readiness probes reflect real UI dependencies.
package ui
