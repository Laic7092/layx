// ftdump: FreeType's bitmap of each glyph on the same canvas as layx-raster's
// coverage, for the fidelity comparison.
//
// 80x80, the pen at (20,20), no hinting: the layout matches TestGlyphFidelity
// and the Coverage dumps, so the two can be differenced against a supersampled
// ground truth. PGM out, one file per gid.
//
//   cc -O2 -o ftdump ftdump.c $(pkg-config --cflags --libs freetype2)
//   ./ftdump font.ttc gids.txt 34 /tmp/ref
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <ft2build.h>
#include FT_FREETYPE_H

int main(int argc, char **argv) {
  const int N = 80, PEN = 20;
  if (argc < 5) { fprintf(stderr, "usage: %s font.ttc gids.txt size outdir\n", argv[0]); return 2; }
  FT_Library lib; FT_Face face;
  if (FT_Init_FreeType(&lib) || FT_New_Face(lib, argv[1], 0, &face)) return 1;
  FT_Set_Pixel_Sizes(face, 0, atoi(argv[3]));
  FILE *f = fopen(argv[2], "r");
  int gid;
  while (fscanf(f, "%d", &gid) == 1) {
    unsigned char *img = calloc(N * N, 1);
    if (!FT_Load_Glyph(face, gid, FT_LOAD_NO_HINTING)) {
      FT_Render_Glyph(face->glyph, FT_RENDER_MODE_NORMAL);
      FT_Bitmap *b = &face->glyph->bitmap;
      int ox = PEN + face->glyph->bitmap_left, oy = PEN - face->glyph->bitmap_top;
      for (unsigned y = 0; y < b->rows; y++) {
        for (unsigned x = 0; x < b->width; x++) {
          int px = ox + (int)x, py = oy + (int)y;
          if (px < 0 || py < 0 || px >= N || py >= N) continue;
          img[py * N + px] = b->buffer[y * b->pitch + x];
        }
      }
    }
    char path[256];
    snprintf(path, sizeof path, "%s/ft-%d.pgm", argv[4], gid);
    FILE *o = fopen(path, "wb");
    fprintf(o, "P5\n%d %d\n255\n", N, N);
    fwrite(img, 1, N * N, o);
    fclose(o);
    free(img);
  }
  fclose(f);
  return 0;
}
