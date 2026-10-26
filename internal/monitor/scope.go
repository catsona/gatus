package monitor

import (
	"github.com/catsona/gstatus/internal/config"
	"github.com/catsona/gstatus/internal/model"
)

func monitored(pr model.PR, cfg config.Config) bool {
	return inScope(pr.Repository, cfg.Repositories) && pr.MatchesState(cfg.View.Filter)
}
