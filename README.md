# seccam-web

Seccam-web is the receiving end for [seccam][0]. This web application accepts event information (video & images), converts, saves and presents them for viewing.

### Requirements & Installing

1. gcc is required
2. `go get -u github.com/battleroid/seccam-web`
3. Before running `seccam-web` you need to copy the templates directory wherever you wish to run the application. The other directories and files are created on the first run.

#### Optional

* Install ffmpeg if you wish for videos to be converted. If it is not installed it will use the existing video.
* Twilio is optional, but it will report it cannot send an SMS when a new event is finalized.

### Parameters

Parameter | Default | Help
--- | --- | ---
-db | `./events.db` | Database location.
-data | `data` | Data (videos & images) location.
-addr | `:8000` | Address for web application to attach to.
-sid | *n/a* | Twilio SID
-token | *n/a* | Twilio auth token
-from | *n/a* | From number
-to | *n/a* | To number
-tmpl | `tmpl` | Template directory.

[0]: https://github.com/Battleroid/seccam