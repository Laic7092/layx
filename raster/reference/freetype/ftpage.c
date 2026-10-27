// ftpage: the same page, rasterized by FreeType.
//
// Composites a file of glyph runs (gid x y size, in device pixels) with
// FreeType's grayscale rasterizer, at the positions layx's layout put them, so
// the result can be compared pixel for pixel with what PageRenderer writes.
// There is no tone correction here: FreeType's grayscale mode has none, and the
// comparison is of rasterizers.
//
//   cc -O2 -o ftpage ftpage.c $(pkg-config --cflags --libs freetype2)
//   ./ftpage font.ttc runs.txt 840 1200 ffffff 000000 out.ppm nohint
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>
#include <time.h>
#include <ft2build.h>
#include FT_FREETYPE_H

static unsigned char *img; static int W, H;

// hex3 parses "rrggbb" into three bytes; scanf's %2x stops short of that.
static void hex3(const char *s, unsigned char out[3]) {
  char b[3] = {0, 0, 0};
  for (int i = 0; i < 3; i++) {
    b[0] = s[i * 2];
    b[1] = s[i * 2 + 1];
    out[i] = (unsigned char)strtol(b, NULL, 16);
  }
}

static void blend(int x, int y, unsigned char ink[3], unsigned char paper[3], unsigned a) {
  if (a == 0) return;
  if (x < 0 || y < 0 || x >= W || y >= H) return;
  unsigned char *p = img + (y * W + x) * 3;
  for (int k = 0; k < 3; k++) {
    unsigned v = (unsigned)ink[k] * a + (unsigned)p[k] * (255 - a) + 127;
    p[k] = (unsigned char)((v + (v >> 8)) >> 8);
  }
}

int main(int argc, char **argv) {
  if (argc < 8) { fprintf(stderr, "usage: %s font runs w h paper ink out.ppm\n", argv[0]); return 2; }
  W = atoi(argv[3]); H = atoi(argv[4]);
  unsigned char paper[3], ink[3];
  hex3(argv[5], paper);
  hex3(argv[6], ink);
  img = malloc((size_t)W * H * 3);
  for (long i = 0; i < (long)W * H; i++) memcpy(img + i * 3, paper, 3);

  FT_Library lib; FT_Face face;
  if (FT_Init_FreeType(&lib) || FT_New_Face(lib, argv[1], 0, &face)) { fprintf(stderr, "freetype\n"); return 1; }
  int load = FT_LOAD_DEFAULT;
  const char *mode = argc > 8 ? argv[8] : "hint";
  if (!strcmp(mode, "nohint")) { load = FT_LOAD_NO_HINTING; FT_Load_Glyph(face, 0, 0); }
  else if (!strcmp(mode, "light")) { load = FT_LOAD_TARGET_LIGHT; }
  struct timespec t0, t1;
  clock_gettime(CLOCK_MONOTONIC, &t0);
  FILE *f = fopen(argv[2], "r");
  if (!f) { perror("runs"); return 2; }
  int gid, px = -1, py = -1, size = 0, fails = 0, drawn = 0;
  fprintf(stderr, "num_glyphs=%ld\n", face->num_glyphs);
  while (fscanf(f, "%d %d %d %d", &gid, &px, &py, &size) == 4) {
    FT_Set_Pixel_Sizes(face, 0, size);
    if (FT_Load_Glyph(face, gid, load)) { if (fails++ < 3) fprintf(stderr, "load %d failed\n", gid); continue; }
    if (FT_Render_Glyph(face->glyph, FT_RENDER_MODE_NORMAL)) continue;
    FT_Bitmap *b = &face->glyph->bitmap;
    if (b->pixel_mode != FT_PIXEL_MODE_GRAY) { fprintf(stderr, "not gray\n"); return 1; }
    drawn++;
    int ox = px + face->glyph->bitmap_left;
    int oy = py - face->glyph->bitmap_top;
    for (unsigned y = 0; y < b->rows; y++)
      for (unsigned x = 0; x < b->width; x++)
        blend(ox + (int)x, oy + (int)y, ink, paper, b->buffer[y * b->pitch + x]);
  }
  fclose(f);
  clock_gettime(CLOCK_MONOTONIC, &t1);
  double ms = (t1.tv_sec - t0.tv_sec) * 1e3 + (t1.tv_nsec - t0.tv_nsec) / 1e6;
  FILE *o = fopen(argv[7], "wb");
  fprintf(o, "P6\n%d %d\n255\n", W, H);
  fwrite(img, 1, (size_t)W * H * 3, o);
  fclose(o);
  printf("wrote %s %dx%d drawn=%d fails=%d raster+blend=%.2f ms\n", argv[7], W, H, drawn, fails, ms);
  return 0;
}
