v9
--

The patch should apply cleanly to the latest (as of 12/26/2017 -- see https://omahaproxy.appspot.com/) release version of Chromium (63.0.3239.108) and v8 (6.3.292.48). The v9_7.0.patch should apply cleanly to v8 version 7.0.276.28.

To obtain a local copy of the v8 source code do the following:

    mkdir v9 && cd v9
    fetch v8 && cd v8           # see https://github.com/v8/v8/wiki/Building-from-Source
    git checkout 6.3.292.48
    gclient sync
    patch -p1 < /path/to/v9.patch
    ./tools/dev/v8gen.py x64.debug
    ninja -C out.gn/x64.debug

You can also build Chromium from souce, although it should not be required to solve the challenge. Use git tag 63.0.3239.108 for that and see https://chromium.googlesource.com/chromium/src/+/lkcr/docs/linux_build_instructions.md.

I used the following args.gn file:

    is_debug = false
    symbol_level = 2

The chrome binary in the release package has been stripped. However, you can download the fully symbolized (5.2GB) binary from https://34c3ctf.ccc.ac/uploads/chrome-df7710b0d52079fed45c39a9157a22390505bb68.elf.

The dockerimage/ directory contains everything you need to reproduce the container setup that is used by the challenge server. The server will start chromium like this: `chromium-browser --headless --disable-gpu --no-sandbox --virtual-time-budget=60000 $URL`. The container is given 2 cores and 8GB of RAM.
