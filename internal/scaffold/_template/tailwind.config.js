// Tailwind v4 is configured in static/input.css (@source, @theme), which loads this file with @config.
// Use it for anything that still needs JavaScript. It deliberately imports nothing, so the standalone
// Tailwind CLI can read it without Node or npm.
export default {
  content: ["./internal/view/*.templ", "./internal/styles/*.go"],
  plugins: [],
};
