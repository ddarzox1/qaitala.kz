// Package qaitala держит ресурсы, вшитые в бинарник: фронтенд и миграции.
// Пакет лежит в корне модуля, потому что go:embed не умеет подниматься вверх
// по дереву, а index.html должен остаться на виду.
package qaitala

import "embed"

//go:embed index.html
var IndexHTML []byte

//go:embed admin.html
var AdminHTML []byte

//go:embed assets/qaitala-social-logo-1080.png
var SocialImage []byte

//go:embed migrations/*.sql
var Migrations embed.FS
