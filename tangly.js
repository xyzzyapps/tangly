// The tangly, built live.
//
// This file is the creature's definition, and it reloads by itself whenever you
// save it. Everything in here can also be typed at the prompt while the pet is
// running, and the change appears on the desktop immediately:
//
//   tangly.leg({angle: 200, reach: 160})    add a leg
//   tangly.color('ring', [255, 90, 90, 255])
//   tangly.sound('step', {pitch: 2})        higher footsteps
//   tangly.gait({speed: 140})               walk faster
//   tangly.save()                           write this file back out
//
// Type help() at the prompt for the whole API, or read TUTORIAL.md.

// The body is a rectangle: half its length along the body axis, half its width
// across it.
tangly.body({length: 16, width: 11});

// A leg is a direction around the body, in degrees from the head, and a natural
// length in pixels. Ten legs, exactly 36 degrees apart all the way round, so
// they stay evenly spread including across the head and the tail. The reaches
// vary so the outline is not a perfect circle.
tangly.legs([
  {angle: 18, reach: 126},
  {angle: 54, reach: 118},
  {angle: 90, reach: 102},
  {angle: 126, reach: 114},
  {angle: 162, reach: 124},
  {angle: -162, reach: 124},
  {angle: -126, reach: 114},
  {angle: -90, reach: 102},
  {angle: -54, reach: 118},
  {angle: -18, reach: 126},
]);

// Strands of silk to trail about, and how it walks.
tangly.silk({count: 9, length: 300});
tangly.gait({speed: 85, agility: 7, turnRate: 4, arrive: 16, drag: 3.2, shinBend: 2.15});
