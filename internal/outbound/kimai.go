package outbound

import (
	"context"
	"net/http"
	"strconv"

	"dashboard/internal/drivers/services"
)

// KimaiStart starts a timer for project and activity (Kimai sets "now").
func KimaiStart(ctx context.Context, baseURL, token string, verifyTLS bool, projectID, activityID int64) error {
	api := services.KimaiApi{URL: baseURL, Token: token, Verify: verifyTLS}
	_, err := api.Send(ctx, http.MethodPost, "timesheets", map[string]any{"project": projectID, "activity": activityID})
	return err
}

// KimaiDescribe sets a timesheet's description.
func KimaiDescribe(ctx context.Context, baseURL, token string, verifyTLS bool, timesheetID int64, description string) error {
	api := services.KimaiApi{URL: baseURL, Token: token, Verify: verifyTLS}
	_, err := api.Send(ctx, http.MethodPatch, "timesheets/"+strconv.FormatInt(timesheetID, 10), map[string]any{"description": description})
	return err
}

// KimaiStop stops one running timesheet.
func KimaiStop(ctx context.Context, baseURL, token string, verifyTLS bool, timesheetID int64) error {
	api := services.KimaiApi{URL: baseURL, Token: token, Verify: verifyTLS}
	_, err := api.Send(ctx, http.MethodPatch, "timesheets/"+strconv.FormatInt(timesheetID, 10)+"/stop", nil)
	return err
}

// KimaiMarkExported flips a timesheet's export flag; call it only for
// sheets that are not exported yet.
func KimaiMarkExported(ctx context.Context, baseURL, token string, verifyTLS bool, timesheetID int64) error {
	api := services.KimaiApi{URL: baseURL, Token: token, Verify: verifyTLS}
	_, err := api.Send(ctx, http.MethodPatch, "timesheets/"+strconv.FormatInt(timesheetID, 10)+"/export", nil)
	return err
}
