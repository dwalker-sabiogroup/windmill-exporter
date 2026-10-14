package windmill

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

func (client *Client) ListWorkers(ctx context.Context, limit int) (*[]Worker, int, error) {
	if limit < 1 {
		return nil, 0, &APIError{Kind: "invalid_request"}
	}
	pageSize := min(100, limit+1)
	rows := []Worker{}
	for page := 1; ; page++ {
		data, status, err := client.Get(ctx, "workers", url.Values{
			"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(pageSize)},
		})
		if err != nil {
			return nil, status, err
		}
		batch := *data.(*[]Worker)
		rows = append(rows, batch...)
		if len(rows) > limit {
			return nil, status, &APIError{Kind: "worker_limit_exceeded", Status: status}
		}
		if !uniqueRows(&rows) {
			return nil, status, &APIError{Kind: "invalid_response", Status: status}
		}
		if len(batch) < pageSize {
			return &rows, status, nil
		}
	}
}

func (client *Client) JobOutcomes(ctx context.Context, workspaces []string, window time.Duration, tagLimit int) (*JobOutcomes, int, error) {
	if len(workspaces) == 0 || window < time.Second || tagLimit < 1 {
		return nil, 0, &APIError{Kind: "invalid_request"}
	}
	horizon := strconv.FormatInt(int64(window/time.Second), 10)
	rows := JobOutcomes{}
	for _, workspace := range workspaces {
		row := JobOutcome{Workspace: workspace}
		for _, success := range []bool{true, false} {
			data, status, err := client.Get(ctx, "job_count", url.Values{
				"workspace": {workspace}, "success": {strconv.FormatBool(success)}, "completed_after_s_ago": {horizon},
			})
			if err != nil {
				return nil, status, err
			}
			count := data.(*Required[int64]).Value
			if success {
				row.Succeeded = count
			} else {
				row.Failed = count
			}
		}
		data, status, err := client.Get(ctx, "job_tags", url.Values{"workspace_id": {workspace}, "horizon_secs": {horizon}})
		if err != nil {
			return nil, status, err
		}
		row.Tags = *data.(*[]CompletedTag)
		if len(row.Tags) > tagLimit {
			return nil, status, &APIError{Kind: "job_tag_limit_exceeded", Status: status}
		}
		rows = append(rows, row)
	}
	return &rows, 200, nil
}

func (client *Client) WorkspaceQueues(ctx context.Context, workspaces []string) (*WorkspaceQueues, int, error) {
	if len(workspaces) == 0 {
		return nil, 0, &APIError{Kind: "invalid_request"}
	}
	rows := WorkspaceQueues{}
	for _, workspace := range workspaces {
		data, status, err := client.Get(ctx, "workspace_queue", url.Values{"workspace": {workspace}})
		if err != nil {
			return nil, status, err
		}
		rows = append(rows, WorkspaceQueue{Workspace: workspace, Inventory: *data.(*QueueInventory)})
	}
	return &rows, 200, nil
}
