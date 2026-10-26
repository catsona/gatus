package monitor

import (
	"github.com/catsona/gatus/internal/config"
	"github.com/catsona/gatus/internal/model"
)

func monitored(pr model.PR, cfg config.Config) bool {
	return inScope(pr.Repository, cfg.Repositories) && pr.MatchesState(cfg.View.Filter)
}
