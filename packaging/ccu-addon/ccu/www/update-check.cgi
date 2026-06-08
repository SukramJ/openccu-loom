#!/usr/bin/env tclsh
# SPDX-License-Identifier: MIT
# Add-on "Update" entry: report the latest published OpenCCU-Loom release
# tag (so the CCU add-on page can show "update available"), or forward to
# the releases page when invoked with cmd=download.
set downloadCmd [regexp {\mcmd=download\M} $env(QUERY_STRING)]
if {$downloadCmd} {
  puts -nonewline "Content-Type: text/html; charset=utf-8\r\n\r\n"
  puts "<html><head><meta http-equiv='refresh' content='0; url=https://github.com/SukramJ/openccu-loom/releases' /></head></html>"
} else {
  set infoUrl https://api.github.com/repos/SukramJ/openccu-loom/releases/latest
  set infoError [catch {
    set info [exec wget -q -O- --no-check-certificate $infoUrl]
    set found [regexp {\"tag_name\"\s*:\s*\"v?([^\"]*)\"} $info -> version]
    if {!$found} error
  }]
  puts -nonewline "Content-Type: text/plain; charset=utf-8\r\n\r\n"
  if {$infoError} {
    puts "N/A"
  } else {
    puts $version
  }
}
