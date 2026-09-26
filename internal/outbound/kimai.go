package outbound

import (
	"context"
	"net/http"
	"strconv"

	"dashboard/internal/drivers/services"
)

// KimaiStart starts a timer for project and activity (Kimai sets "now").
func KimaiStart(ctx context.Context, to Target, projectID, activityID int64) error {
	api := services.KimaiApi{URL: to.URL, Token: to.Token, Verify: to.VerifyTLS}
	_, err := api.Send(ctx, http.MethodPost, "timesheets", map[string]any{"project": projectID, "activity": activityID})
	return err
}

// KimaiCreate books a finished timesheet; begin and end are local times
// ("2026-09-26T09:05:00"), as Kimai reads them in the user's timezone.
func KimaiCreate(ctx context.Context, to Target, projectID, activityID int64, begin, end, description string) error {
	api := services.KimaiApi{URL: to.URL, Token: to.Token, Verify: to.VerifyTLS}
	body := map[string]any{"project": projectID, "activity": activityID, "begin": begin, "end": end}
	if description != "" {
		body["description"] = description
	}
	_, err := api.Send(ctx, http.MethodPost, "timesheets", body)
	return err
}

// KimaiDescribe sets a timesheet's description.
func KimaiDescribe(ctx context.Context, to Target, timesheetID int64, description string) error {
	api := services.KimaiApi{URL: to.URL, Token: to.Token, Verify: to.VerifyTLS}
	_, err := api.Send(ctx, http.MethodPatch, "timesheets/"+strconv.FormatInt(timesheetID, 10), map[string]any{"description": description})
	return err
}

// KimaiStop stops one running timesheet.
func KimaiStop(ctx context.Context, to Target, timesheetID int64) error {
	api := services.KimaiApi{URL: to.URL, Token: to.Token, Verify: to.VerifyTLS}
	_, err := api.Send(ctx, http.MethodPatch, "timesheets/"+strconv.FormatInt(timesheetID, 10)+"/stop", nil)
	return err
}

// KimaiMarkExported flips a timesheet's export flag; call it only for
// sheets that are not exported yet.
func KimaiMarkExported(ctx context.Context, to Target, timesheetID int64) error {
	api := services.KimaiApi{URL: to.URL, Token: to.Token, Verify: to.VerifyTLS}
	_, err := api.Send(ctx, http.MethodPatch, "timesheets/"+strconv.FormatInt(timesheetID, 10)+"/export", nil)
	return err
}
