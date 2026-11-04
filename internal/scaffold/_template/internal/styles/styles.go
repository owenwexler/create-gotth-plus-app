package styles

// Tailwind only generates classes it can find as complete literal strings, so every class here is
// spelled out in full (static/input.css lists this file as a source).

func GenerateBigButtonClasses() string {
	return "bg-linear-to-b from-neutral-700 to-neutral-900 text-white min-h-11 px-5 py-2.5 rounded-xl border border-white/15 text-sm font-medium shadow-sm cursor-pointer hover:from-neutral-600 hover:to-neutral-800 focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white active:scale-95 motion-safe:transition duration-150 disabled:cursor-wait disabled:opacity-60"
}

func GenerateSmallButtonClasses() string {
	return "bg-linear-to-b from-neutral-700 to-neutral-900 text-white px-4 py-1.5 rounded-lg border border-white/15 text-sm font-medium cursor-pointer hover:from-neutral-600 hover:to-neutral-800 focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white active:scale-95 motion-safe:transition duration-150 disabled:cursor-wait disabled:opacity-60"
}

func GenerateIconButtonClasses() string {
	return "text-white hover:text-neutral-300 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-white cursor-pointer active:scale-95 motion-safe:transition duration-150"
}

func GenerateSVGImageClasses() string {
	return "h-5 w-5 fill-white hover:fill-neutral-300"
}

func GenerateLinkButtonClasses() string {
	return "bg-transparent text-white underline underline-offset-4 cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-white active:scale-95 motion-safe:transition duration-150"
}

func GenerateFormDivClasses() string {
	return "flex flex-col space-y-2 items-center justify-center pb-2"
}

func GenerateHorizontalFormClasses() string {
	return "mt-auto flex w-full flex-col gap-3 pt-4 font-sans sm:flex-row sm:items-center"
}

func GenerateInputClasses() string {
	return "min-w-0 w-full flex-1 rounded-xl bg-black/30 px-4 py-3 text-white placeholder-neutral-400 border border-white/20 text-base focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-white"
}

func GenerateDialogClasses() string {
	return "overflow-visible fixed inset-0 z-50 m-auto p-6 rounded-2xl border border-white/15 bg-linear-to-br from-neutral-800 to-neutral-900 text-white shadow-xl backdrop:bg-black/70"
}

func GenerateCardClasses() string {
	return "w-full min-w-0 flex flex-col gap-4 rounded-3xl border border-white/15 bg-linear-to-br from-neutral-800 to-neutral-900 p-6 shadow-2xl shadow-black/30 sm:p-8"
}
