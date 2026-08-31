package main

import (
	"fmt"
	"io"
	"time"
)

func printReportTable(out io.Writer, report Report) {
	fmt.Fprintf(out, "LLMConform · %s · %s\n\n", report.BaseURL, report.Model)
	fmt.Fprintln(out, "ROUTE             BASIC    STREAM   TOOLS    USAGE    ERRORS")
	for _, route := range report.Routes {
		statuses := make(map[string]string, len(route.Checks))
		for _, check := range route.Checks {
			statuses[check.ID] = check.Status
		}
		fmt.Fprintf(out, "%-17s %-8s %-8s %-8s %-8s %-8s\n",
			route.Name,
			statuses[CheckBasic], statuses[CheckStream], statuses[CheckTools],
			statuses[CheckUsage], statuses[CheckErrors],
		)
	}
	duration := time.Since(report.StartedAt).Round(time.Millisecond)
	if report.FinishedAt != nil {
		duration = report.FinishedAt.Sub(report.StartedAt).Round(time.Millisecond)
	}
	fmt.Fprintf(out, "\n%d passed · %d warnings · %d failed · %s\n", report.Summary.Pass, report.Summary.Warn, report.Summary.Fail, duration)
}
