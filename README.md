# audiout

`audiout` is a simple CLI tool for switching between audio devices on a mac

## installation

Depends on `fzf` and `SwitchAudioSource`, install with `brew`:

```sh
brew install fzf switchaudio-osx
go install
```

After that's done, just `go install`.

## configuration

Optionally, you can configure `audiout` with a YAML file.

```yaml
# friendly rewrites the name of an audio device so it looks nicer in the UI
friendly:
  "UMC404HD 192k": "DAC"
  "MacBook Pro Speakers": "Laptop speakers"
# ignored filters an audio device out of the app entirely
ignored:
  - "U32V3"
  - "ZoomAudioDevice"
  - "CalDigit Thunderbolt 3 Audio"
```

Point `audiout` at the config by setting the `AUDIOUT_CONFIG` environment variable. The default location is `~/.config/.audiout.yaml`.
