# Game Boy Run Length Encoding format

To reduce data load during WebSocket streaming, a run length encoding format is being used.

## Game Boy 1989 (DMG)
To compress four color Game Boy images, the following format is being used:

| Code           | Command                                                                   |
|----------------|---------------------------------------------------------------------------|
| 0x00 - 0x03    | Regular pixel color value                                                 |
| 0x04 - 0xA3    | Line identifier (with an offset of 4)                                     |
| 0xDX 0xYY      | Repeat the following byte (YY) XX times                                   |
| 0xF0           | Start regular non-compressed line                                         |
| 0xF1           | Start compressed line                                                     |
| 0xFA           | Begin compressed full screen                                              |
| 0xFB           | Begin compressed delta screen                                             |
| 0xF2 0xXX 0xYY | Repeat the following byte (YY) XX times                                   |
| 0xFD 0xXX      | Repeat the following byte (XX) until end of line                          |
| 0xFE           | Line hasn't changed since last screen update (Used in delta compression)  |
| 0xFF           | Pixel hasn't changed since last screen update (Used in delta compression) |


## Game Boy Color (CGB)
For CGB compression the following format can be used:

| Code                          | Command                                                                  |
|-------------------------------|--------------------------------------------------------------------------|
| 0x00 - 0x37                   | Regular pixel color value                                                |
| 0x38 - 0xC8                   | Line identifier (with an offset of 56)                                   |
| 0xDX 0xYY                     | Repeat the following byte (YY) XX times                                  |
| 0xE1 0xXX                     | Switch to color palette XX                                               |
| 0xEA 0xXX 0xYY                | Begin definition for color palette XX with length YY                     |
| (0x00 - 0x37) 0xXX 0xYY 0xZZ  | Within palette declaration: Define color with RGB values XX YY ZZ        |
| 0xEB                          | Begin compressed full screen (CGB)                                       |
| 0xEC                          | Begin compressed delta screen (CGB)                                      |
| 0xED 0xVV 0xWW 0xXX 0xYY 0xZZ | Update color palette VV color WW with RGB values XX YY ZZ                |
| 0xF0                          | Start regular non-compressed line                                        |
| 0xF1                          | Start compressed line                                                    |
| 0xF2 0xXX 0xYY                | Repeat the following byte (YY) XX times                                  |
| 0xFD 0xXX                     | Repeat the following byte (XX) until end of line                         |
| 0xFE                          | Line hasn't changed since last screen update (Used in delta compression) |


## Example for a line
| Line identifier | Start of compressed delta line | Pixel value | Repeat 4 times | Pixel value | Repeat until EOL | No change |
|-----------------|--------------------------------|-------------|----------------|-------------|------------------|-----------|
| 0x04            | 0xF1                           | 0x01        | 0xC4           | 0x00        | 0xFD             | 0xFF      |
