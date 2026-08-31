package main

import (
	"fmt"
	"io"
	"time"
)

func printReportTable(out io.Writer, report Report) {
	fmt.Fprintf(out, "LLMConform · %s · %s\n", report.BaseURL, report.Model)
	fmt.Fprintf(out, "Profile: %s · Level: %s · Catalog: %s\n\n", report.Plan.Profile, report.Plan.Level, report.CatalogVersion)
	fmt.Fprintln(out, "ROUTE             STATUS     PASS  WARN  FAIL  BLOCKED  ERROR")
	for _, route := range report.Routes {
		var counts StatusCounts
		for _, result := range route.Cases {
			incrementStatus(&counts, result.Status)
		}
		fmt.Fprintf(out, "%-17s %-10s %-5d %-5d %-5d %-8d %d\n",
			route.Name, route.Status, counts.Pass, counts.Warn, counts.Fail, counts.Blocked, counts.Error,
		)
	}

	printedHeading := false
	for _, route := range report.Routes {
		for _, result := range route.Cases {
			if result.Status != StatusFail && result.Status != StatusWarn && result.Status != StatusError {
				continue
			}
			if !printedHeading {
				fmt.Fprintln(out, "\nFindings:")
				printedHeading = true
			}
			fmt.Fprintf(out, "- %s / %s: %s (%s)\n", route.Name, result.Name, result.Summary, result.ReasonCode)
		}
	}

	duration := time.Since(report.StartedAt).Round(time.Millisecond)
	if report.FinishedAt != nil {
		duration = report.FinishedAt.Sub(report.StartedAt).Round(time.Millisecond)
	}
	fmt.Fprintf(out, "\n%d passed · %d warnings · %d failed · %d blocked · %d errors · %s\n",
		report.Summary.Pass,
		report.Summary.Warn,
		report.Summary.Fail,
		report.Summary.Blocked,
		report.Summary.Error,
		duration,
	)
}
