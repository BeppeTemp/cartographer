import type { NodeHoverDrawingFunction } from "sigma/rendering";

/**
 * The hover and selection halo, drawn on Sigma's 2D hover layer.
 *
 * Stock Sigma's hover is a white box behind the label, which on the dark
 * signature theme reads as a hole punched in the canvas. This draws what the
 * motion spec asks for instead: a soft glow and a ring in the node's own
 * colour (the accent, for the selected node, since that is the colour
 * selection gives it), with the label on a token-coloured pill.
 *
 * It is the 2D layer, not a WebGL program, so it needs no node program beyond
 * the stock "circle" (amendment 1): Sigma calls it only for the hovered node
 * and for nodes marked `highlighted`, which keeps it cheap on any graph size.
 */
export interface HaloColors {
  labelBackground: string;
  labelBorder: string;
  labelText: string;
}

/** Pixels between the node's edge and its ring. */
const RING_GAP = 3;
/** Radius of the soft glow beyond the node's edge. */
const GLOW = 11;
const GLOW_ALPHA = 0.22;

export function makeHoverDrawer(colors: HaloColors): NodeHoverDrawingFunction {
  return (context, data, settings) => {
    const { x, y, size, color, label } = data;

    context.save();
    context.globalAlpha = GLOW_ALPHA;
    context.fillStyle = color;
    context.beginPath();
    context.arc(x, y, size + GLOW, 0, Math.PI * 2);
    context.fill();

    context.globalAlpha = 1;
    context.lineWidth = 2;
    context.strokeStyle = color;
    context.beginPath();
    context.arc(x, y, size + RING_GAP, 0, Math.PI * 2);
    context.stroke();
    context.restore();

    if (!label) return;
    const fontSize = settings.labelSize;
    context.font = `${settings.labelWeight} ${fontSize}px ${settings.labelFont}`;
    const width = context.measureText(label).width;
    const padX = 6;
    const height = fontSize + 8;
    const left = x + size + RING_GAP + 6;
    const top = y - height / 2;

    context.fillStyle = colors.labelBackground;
    context.strokeStyle = colors.labelBorder;
    context.lineWidth = 1;
    context.beginPath();
    roundedRect(context, left, top, width + padX * 2, height, height / 2);
    context.fill();
    context.stroke();

    context.fillStyle = colors.labelText;
    context.textBaseline = "middle";
    context.fillText(label, left + padX, y + 0.5);
    context.textBaseline = "alphabetic";
  };
}

function roundedRect(
  context: CanvasRenderingContext2D,
  x: number,
  y: number,
  width: number,
  height: number,
  radius: number,
) {
  const r = Math.min(radius, width / 2, height / 2);
  context.moveTo(x + r, y);
  context.arcTo(x + width, y, x + width, y + height, r);
  context.arcTo(x + width, y + height, x, y + height, r);
  context.arcTo(x, y + height, x, y, r);
  context.arcTo(x, y, x + width, y, r);
  context.closePath();
}
