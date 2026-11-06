package optimistic

import (
	"encoding/json"
	"strconv"
)

// OptimisticFlip builds the hx-optimistic config for a toggle.  The hx-target carries its state as
// data-<key>, so flipping that one attribute on the target is the whole optimistic
// update.  "revert" isn't read by the extension; the htmx:beforeSwap handler in app.js uses it
// to roll back when the server answers with an error toast (the extension's own rollback never
// runs for those, because that handler suppresses htmx:responseError).
func OptimisticFlip(key string, current bool) string {
	attr := "data-" + key

	config, _ := json.Marshal(map[string]any{
		"values": map[string]string{attr: strconv.FormatBool(!current)},
		"revert": map[string]string{attr: strconv.FormatBool(current)},
		// Network failures are still rolled back by the extension.  Keep its error output invisible,
		// so no layout needs to make room for a message.
		"errorMode":     "append",
		"errorTemplate": "<span hidden></span>",
		"delay":         1,
	})

	return string(config)
}
