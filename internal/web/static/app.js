// Refresh on a timer rather than polling fragments: the whole page is cheap and
// this keeps the wall honest without a JS framework. A file rather than an inline
// <script> so the engine's CSP can stay script-src 'self'.
setTimeout(function () { location.reload(); }, 10000);
