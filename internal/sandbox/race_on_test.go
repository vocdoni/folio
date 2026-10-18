// SPDX-License-Identifier: AGPL-3.0-only

//go:build race

package sandbox

// The race detector reserves terabytes of shadow address space, so helper
// processes built from this test binary cannot run under RLIMIT_AS.
const raceEnabled = true
