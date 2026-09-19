// SPDX-License-Identifier: MIT

package daemon

import (
	"errors"
	"io"
	"log/slog"
	"testing"
)

func TestStorageHeartbeatDoesNotHideRejectedIngest(t *testing.T) {
	app := &App{options: Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	app.recordWrite(errors.New("synthetic storage failure"), "traffic", true)
	app.recordWrite(nil, "coverage", false)
	app.recordWrite(nil, "interface", true)
	if app.storageHealth.Healthy || len(app.storageHealth.FailedOperations) != 1 || app.storageHealth.FailedOperations[0] != "traffic" {
		t.Fatalf("unrelated write hid ingest failure: %+v", app.storageHealth)
	}
	app.recordWrite(nil, "traffic", true)
	if !app.storageHealth.Healthy || app.storageHealth.Failures != 1 || app.storageHealth.LastFailure.IsZero() || app.storageHealth.LastDurableIngest.IsZero() {
		t.Fatalf("recovery lost historical failure evidence: %+v", app.storageHealth)
	}
}
