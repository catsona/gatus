package monitor

import (
	"git.pegasko.art/catsona/gstatus/internal/config"
	"git.pegasko.art/catsona/gstatus/internal/model"
)

func monitored(pr model.PR, cfg config.Config) bool {
	return inScope(pr.Repository, cfg.Repositories) && pr.MatchesState(cfg.View.Filter)
}
