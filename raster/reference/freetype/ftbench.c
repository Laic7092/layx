// ftbench: the platform's cost for the same job layx-raster does, per glyph.
//
// Loads and renders every glyph of a gid list with FreeType's grayscale
// rasterizer and prints the average, so a page's glyph work can be put next to
// BenchmarkGlyphRasterizer. FreeType is not a dependency of layx-raster; this is
// a yardstick, built by hand (see README.md).
//
//   cc -O2 -o ftbench ftbench.c $(pkg-config --cflags --libs freetype2)
//   ./ftbench /usr/share/fonts/truetype/wqy/wqy-microhei.ttc 34 gids.txt 40
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <ft2build.h>
#include FT_FREETYPE_H
#include FT_GLYPH_H

static double now(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return ts.tv_sec + ts.tv_nsec * 1e-9;
}

int main(int argc, char **argv) {
  if (argc < 4) { fprintf(stderr, "usage: %s font.ttf px glyph-index-list-file [reps]\n", argv[0]); return 2; }
  const char *fontPath = argv[1];
  int px = atoi(argv[2]);
  int reps = argc > 4 ? atoi(argv[4]) : 20;

  FILE *f = fopen(argv[3], "r");
  if (!f) { perror("gids"); return 2; }
  int gids[100000]; int n = 0, v;
  while (n < 100000 && fscanf(f, "%d", &v) == 1) gids[n++] = v;
  fclose(f);
  if (!n) return 2;

  FT_Library lib;
  if (FT_Init_FreeType(&lib)) return 1;
  FT_Face face;
  if (FT_New_Face(lib, fontPath, 0, &face)) { fprintf(stderr, "face\n"); return 1; }
  FT_Set_Pixel_Sizes(face, 0, px);

  long pixsum = 0;
  for (int r = -1; r < reps; r++) {
    double t0 = now();
    for (int i = 0; i < n; i++) {
      if (FT_Load_Glyph(face, gids[i], FT_LOAD_DEFAULT)) continue;
      if (FT_Render_Glyph(face->glyph, FT_RENDER_MODE_NORMAL)) continue;
      FT_Bitmap *b = &face->glyph->bitmap;
      for (unsigned y = 0; y < b->rows; y++)
        for (unsigned x = 0; x < b->width; x++) pixsum += b->buffer[y * b->pitch + x];
    }
    double dt = now() - t0;
    if (r == reps - 1) printf("freetype %d glyphs at %dpx: %.3f us/glyph (sum %ld)\n", n, px, dt / n * 1e6, pixsum);
  }
  return 0;
}
