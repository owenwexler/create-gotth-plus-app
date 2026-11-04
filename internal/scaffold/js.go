package scaffold

// commitJS stops git-ignoring the downloaded browser libraries, as the template's README and
// SECURITY.md tell a new project to do, so production serves pinned, reviewed copies.
func commitJS(p Project) error {
	return p.edits([]edit{
		{".gitignore",
			"\n# Downloaded by `make js` (download-js.sh). The template does not ship these libraries.\n" +
				"# In your own project, delete the next line and commit them so production serves pinned, reviewed copies.\n" +
				"static/js/*.min.js\n",
			""},
		{"README.md",
			"`.gitignore` excludes `static/js/*.min.js` so the template itself stays free of them. In your own project, remove that line and commit the files, so production serves copies you have pinned and reviewed.\n",
			"This project commits the files (`.gitignore` does not exclude them), so production serves copies you have pinned and reviewed. Never run `download-js.sh` in a production build: by default it fetches the latest versions.\n"},
	})
}
