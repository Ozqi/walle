package runtime

import (
	"strings"

	"github.com/Ozqi/walle/internal/codex"
	"github.com/Ozqi/walle/internal/utils"
)

func modelLoadOptions(modelRef string) utils.LoadConfigOptions {
	options := utils.LoadConfigOptions{ModelRef: modelRef}
	if strings.HasPrefix(modelRef, "openai/") {
		if store, err := codex.DefaultStore(); err == nil && store.LoggedIn() {
			options.LLMFormat = "codex"
		}
	}
	return options
}
