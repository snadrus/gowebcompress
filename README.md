# gowebcompress
Optimal GO compression middlewares.

This repo provides 2 middlewares capable of providing the best of web compression to your Go server:

Dynamic & Static

They both provide Brotli & GZip with the best settings for each scenario.

Ex:
http.ListenAndServe(":80", gowebcompress.Dynamic(http.DefaultRouter))

License: MIT. Use it anywhere. 
I gladly accept your improvement patches.
