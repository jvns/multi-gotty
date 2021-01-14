# multi-gotty

This is a fork of [gotty](https://github.com/yudai/gotty/releases) that lets
`gotty` serve multiple different terminal applications at the same time and
lets you programmatically determine what terminal application `gotty` should serve by
giving it a webserver to use.

I just wrote it for myself to use and don't really plan on maintaining it for anyone
else's use, but maybe it's an interesting example of how to modify `gotty` for
a different use case. I hardcoded all of `gotty`'s command line arguments.

## the JSON format

The `examples/` directory  contains an example of the JSON format gotty expects
from its server: 

```
{"banana": ["htop"], "pomegranate": ["watch", "ls"], "shell": ["bash"]}
```

When I'm using this I actually programmatically generate the JSON file, the
static JSON file is just to demonstrate the format.

### how to use it

Start a server like this:
```
cd examples
python3 -m http.server
```

Then start multi-gotty like this:

```
multi-gotty --port 7777 http://localhost:3000/index.json
```

Then visit  http://localhost:7777/proxy/banana/ in your browser. You should get
`htop`. http://localhost:7777/proxy/shell/ should give you a shell.

