# gowebcompress
*Optimal GO web compression.*

IN DEVELOPMENT:
- Needs more testing
   
This repo provides the best web compression:
- Dynamic & Static


They both provide Brotli & GZip with the best settings for each scenario.

Ex:
```go
http.ListenAndServe(":80", gowebcompress.Dynamic(http.DefaultRouter))

s := gowebcompress.NewStatic("./static")
// In Handler:
s.SendFile(r, w, r.URL.Path)
```

They're cross-compatible & will not share a file you haven't allowed into static or sent directly. 
   
License: MIT. Use it anywhere. 
I gladly accept your improvement patches.
