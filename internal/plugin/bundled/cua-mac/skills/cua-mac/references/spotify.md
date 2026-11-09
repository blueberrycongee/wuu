# Spotify playback and library

Use this guidance after observing the Spotify desktop app. Read the current controls rather than assuming their labels or positions.

- Separate the search results, selected track, Now Playing area, and playback device. A highlighted search result does not establish what is playing.
- Play/pause, shuffle, repeat, and library buttons can toggle state. Send one action, then inspect its resulting label or state before deciding whether another action is needed. Do not replay a toggle because the first observation still shows the old state.
- Search, navigation, and playback state can update asynchronously. When the desired state has a precise AX label or value, use `wait_for` with that observed selector and a bounded timeout. Otherwise take a fresh observation and inspect the Now Playing area. If the result remains unclear, report it as unverified rather than stacking repeated actions.
- Check the active Spotify Connect device before changing playback or volume. The desktop app can control another device; do not switch output to the local Mac unless that is part of the user's request.
- Inspect the final playlist or library entry after adding content. Distinguish an add button's delivery from the item actually appearing, and avoid duplicate additions while the view is updating.

Product behavior reference: [Spotify Connect](https://support.spotify.com/us/article/spotify-connect/).
