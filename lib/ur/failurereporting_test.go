// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package ur

import (
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/events"
	"github.com/syncthing/syncthing/lib/ur/contract"
)

func TestFailureHandlerFollowsUsageReporting(t *testing.T) {
	evLogger := events.NewLogger()
	go evLogger.Serve(t.Context())

	handler := &failureHandler{
		evLogger: evLogger,
		optsChan: make(chan config.OptionsConfiguration, 1),
		buf:      make(map[string]*failureStat),
	}
	on := config.OptionsConfiguration{URAccepted: 3}
	off := config.OptionsConfiguration{URAccepted: -1}

	_, sub, evChan := handler.applyOpts(on, nil)
	if sub == nil || evChan == nil {
		t.Fatal("not listening for failures with usage reporting on")
	}
	handler.addReport(contract.FailureData{Description: "waiting"}, time.Now())

	// Usage reporting going off is all that changes
	handler.CommitConfiguration(config.Configuration{Options: on}, config.Configuration{Options: off})
	select {
	case opts := <-handler.optsChan:
		_, sub, evChan = handler.applyOpts(opts, sub)
	default:
		t.Fatal("the handler was not told")
	}
	if sub != nil || evChan != nil {
		t.Fatal("still listening for failures with usage reporting off")
	}
	if len(handler.buf) != 0 {
		t.Fatal("a failure is still waiting to be sent")
	}
}
