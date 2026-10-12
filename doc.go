// Package themida reads, edits, writes, and applies Themida protection projects.
//
// Themida (by Oreans Technologies) is a Windows software protector. A protection
// project is a .tmd file: an INI-like document under a single [SECURE_ENGINE]
// section whose KEY=VALUE entries configure how an input executable is protected
// and where the protected output is written. The Themida GUI edits these projects
// interactively; this package models the same settings programmatically so that
// protection can be configured and driven from Go, including on non-Windows hosts
// through Wine.
//
// # Project model
//
// [Project] is the root model. It exposes the settings this package understands
// as typed fields ([Options], [MacroOptions], [Messages], [XBundlerOptions]) while
// preserving every unmodeled setting, comment, and the original key order, so a
// parsed project round-trips faithfully and edits stay minimal. Settings
// this package does not model are reachable by their native key through
// [Project.Get], [Project.Set], and [Project.Override] using the untyped
// [Settings] map. Native keys use the exact names Themida writes, for example
// OPTION_PROTECTION_IS_ANTIDEBUG or OPTION_MACROS_ENCRYPT_ANSI_STRINGS.
//
// # Execution
//
// [Runner] applies a project to an input binary by invoking the Themida protector
// with /protect, either natively on Windows or through Wine elsewhere. It supports
// context cancellation, streamed diagnostics, temporary license installation and
// restoration, path conversion for Wine, and cleanup of temporary projects and
// output. It has no external Go dependencies.
//
// The Themida settings, macros, virtual machines, and dialogs referenced
// throughout this package are described in Oreans' Themida documentation at
// https://www.oreans.com/help/tm/index.html.
package themida
