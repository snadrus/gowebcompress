[![](https://godoc.org/github.com/snadrus/gowebcompress?status.svg)](http://godoc.org/github.com/snadrus/gowebcompress) [![Build Status](http://img.shields.io/travis/snadrus/gowebcompress.svg?style=flat-square)](https://travis-ci.org/snadrus/gowebcompress)     [![Coverage Status](https://coveralls.io/repos/github/snadrus/gowebcompress/badge.svg?branch=master)](https://coveralls.io/github/snadrus/gowebcompress?branch=master)    [![Donate](https://www.paypalobjects.com/en_US/i/btn/btn_donate_SM.gif)](https://www.paypal.com/cgi-bin/webscr?cmd=_s-xclick&hosted_button_id=C6284X93YL4WA)
# gowebcompress
*Optimal GO web compression.*

IN DEVELOPMENT:
- Needs more testing
   
This repo provides the best web compression:
- Dynamic & Static


They both provide Brotli & GZip with the best settings for each scenario.

## Examples
```go
http.ListenAndServe(":80", gowebcompress.Dynamic(http.DefaultRouter))

s := gowebcompress.NewStatic("./static")
// In Handler:
s.SendFile(r, w, r.URL.Path)
```
See [Godoc](http://godoc.org/github.com/snadrus/gowebcompress) for more options.

They're cross-compatible & will not share a file you haven't allowed into static or sent directly. 
   
License: MIT. Use it anywhere. 

I gladly accept your improvement patches.
