// Global state shared by every component; read it with $store.globalState in Alpine
// (e.g. x-show="$store.globalState.loading") or Alpine.store('globalState') in plain JS.
// The fields below are examples: replace them with whatever your app needs to share.
document.addEventListener('alpine:init', () => {
    Alpine.store('globalState', {
        // how many htmx requests are in flight (kept up to date by the htmx hooks below)
        pendingRequests: 0,
        get loading() {
            return this.pendingRequests > 0
        },
        // false while the browser is offline
        online: navigator.onLine,
        reset() {
          this.pendingRequests = 0;
          this.online = navigator.onLine;
        }
    })
})

// htmx skips swapping error responses by default.  The server marks the error responses meant for the
// user with HX-Reswap: toasts come back with HX-Reswap: none (the swap only processes the out of band
// toast), and error pages with HX-Reswap: innerHTML (they replace the request's target).
document.addEventListener('htmx:beforeSwap', (e) => {
    const xhr = e.detail.xhr

    if (xhr.status >= 400 && xhr.getResponseHeader('HX-Reswap')) {
        e.detail.shouldSwap = true
        e.detail.isError = false

        // hx-optimistic only rolls back on htmx:responseError, which isError = false
        // suppresses, so undo the optimistic update here.  Its config (on the element that
        // made the request) says what to restore on the hx-target.
        const raw = e.detail.requestConfig?.elt?.dataset.optimistic
        const row = e.detail.target

        if (raw && row) {
            try {
                Object.assign(row.dataset, Object.fromEntries(
                    Object.entries(JSON.parse(raw).revert || {}).map(([k, v]) => [k.replace(/^data-/, ''), v])
                ))
            } catch (_) {}

            // A native checkbox flips itself on click, so put it back in line with the row
            const box = row.querySelector('input[type=checkbox]')
            if (box) box.checked = row.dataset.done === 'true'
        }
    }
})

// Keep the globalState store in step with htmx and the browser, so any component can react to it.
document.addEventListener('htmx:beforeRequest', () => {
    if (!window.Alpine) return

    Alpine.store('globalState').pendingRequests++
})

// htmx:afterRequest fires for every request that htmx:beforeRequest did, whether it succeeded or not
document.addEventListener('htmx:afterRequest', () => {
    if (!window.Alpine) return

    const store = Alpine.store('globalState')

    store.pendingRequests = Math.max(0, store.pendingRequests - 1)
})

for (const type of ['online', 'offline']) {
    window.addEventListener(type, () => {
        if (window.Alpine) Alpine.store('globalState').online = navigator.onLine
    })
}

// The CSP build of Alpine cannot evaluate inline objects or statements, so component state and
// behavior is registered here and referenced by name (x-data="dialogs").
document.addEventListener('alpine:init', () => {
    Alpine.data('dialogs', () => ({
        searchopen: false,
        filteropen: false,
    }))

    // Example app: a counter that lives entirely in the browser (see internal/view/example.templ)
    Alpine.data('counter', () => ({
        count: 0,
        increment() {
            this.count++
        },
        decrement() {
            this.count--
        },
        reset() {
            this.count = 0
        },
    }))

    // Toasts fade out and remove themselves after a few seconds
    Alpine.data('toast', () => ({
        show: true,
        init() {
            setTimeout(() => {
                this.show = false
                setTimeout(() => this.$el.remove(), 300)
            }, 4000)
        },
    }))
})

// Error toasts are swapped as if they were successes (see htmx:beforeSwap above), so htmx reports them as
// successful.  Check the status to tell a real success apart.
function succeeded(e) {
    return Boolean(e.detail.xhr) && e.detail.xhr.status < 400
}

// Forms marked data-reset-on-success are cleared once their request succeeds, and keep what the user typed
// when it fails (replaces hx-on::after-request, which needs eval).
document.addEventListener('htmx:afterRequest', (e) => {
    const form = e.target.closest?.('form[data-reset-on-success]')

    if (form && succeeded(e)) form.reset()
})

// Dialogs marked data-close-on-success close once their request succeeds.  On an error they stay open with
// the input focused, so the user can fix it straight away.  (Don't also close them from the submit
// button's click, or they close before the answer arrives.)
document.addEventListener('htmx:afterRequest', (e) => {
    const dialog = e.target.closest?.('dialog[data-close-on-success]')
    if (!dialog) return

    if (succeeded(e)) {
        dialog.close()
    } else {
        dialog.querySelector('input')?.focus()
    }
})

// Requests that never reach the server (offline, server down), or that fail without sending an error
// toast of their own, would otherwise change nothing and say nothing.
function showErrorToast(header, body) {
    const toast = document.createElement('div')
    // the same markup and classes as view.Toast, so it looks and dismisses itself the same way
    toast.setAttribute('x-data', 'toast')
    toast.setAttribute('x-show', 'show')
    toast.setAttribute('x-transition.opacity', '')
    toast.setAttribute('x-on:click', 'show = false')
    toast.setAttribute('role', 'alert')
    toast.className = 'w-72 rounded-lg shadow-lg bg-white border p-3 cursor-pointer border-red-600'

    const title = document.createElement('p')
    title.className = 'font-bold text-red-600'
    title.textContent = header
    const text = document.createElement('p')
    text.className = 'text-sm text-gray-700'
    text.textContent = body
    toast.append(title, text)

    document.getElementById('toasts')?.append(toast)
}

document.addEventListener('htmx:sendError', () => {
    showErrorToast('Could not reach the server', 'Check your connection and try again')
})

document.addEventListener('htmx:responseError', (e) => {
    // responses that carry their own toast or error page are swapped instead (see htmx:beforeSwap)
    if (!e.detail.xhr.getResponseHeader('HX-Reswap')) {
        showErrorToast('Something went wrong', 'Please try again')
    }
})
