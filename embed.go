package main

import "embed"

//go:embed web/static
var webStatic embed.FS

//go:embed web/templates
var webTemplates embed.FS
