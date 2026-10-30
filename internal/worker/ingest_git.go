package worker

import (
	"context"
	"fmt"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

// gitFetcher is the source_fetch of a Source whose connector is "git": the
// locator is "<repo URL>#<path>" as the git-docs recipe writes it, and the
// reader checks the host, the path and symlinks (D1262). The tool still takes no
// argument, so the model cannot aim it elsewhere.
type gitFetcher struct {
	locator string
	reader  *recipe.GitReader
}

func (g *gitFetcher) fetch(ctx context.Context) client.ToolOutput {
	text, cut, err := g.reader.Read(ctx, g.locator, fetchMaxBytes)
	if err != nil {
		return client.ToolOutput{IsError: true, Text: "fetch failed: " + brief(err.Error())}
	}
	if cut {
		text += fmt.Sprintf("\n[truncated: the source continues after byte %d]", fetchMaxBytes)
	}
	return client.ToolOutput{Text: fetchBanner + "\n\n" + text}
}
