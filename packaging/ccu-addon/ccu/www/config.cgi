#!/usr/bin/env tclsh
# SPDX-License-Identifier: MIT
# Add-on "Settings" entry: redirect the operator to the OpenCCU-Loom
# Config UI (the SPA on the REST listener). The daemon is configured
# entirely through this UI; first run lands on the login / setup wizard.
puts -nonewline "Content-Type: text/html; charset=utf-8\r\n\r\n"
puts {<!doctype html>
<html><head>
    <script>
        document.write('<meta http-equiv="refresh" content="0; url=')
        document.write('http://' + window.location.hostname + ':8080/app/')
        document.write('">')
    </script>
</head>
<body>Redirecting to the OpenCCU-Loom Config UI on port 8080 …</body></html>
}
