// The slice of d3-force-3d the 3D view uses. The package ships no types; its
// API is d3-force's, with a z axis and a numDimensions argument.
declare module "d3-force-3d" {
  export interface SimNode {
    index?: number;
    x?: number;
    y?: number;
    z?: number;
    vx?: number;
    vy?: number;
    vz?: number;
    fx?: number;
    fy?: number;
    fz?: number;
  }

  export interface Force<N extends SimNode> {
    (alpha: number): void;
    initialize?(nodes: N[], ...rest: unknown[]): void;
  }

  export interface Simulation<N extends SimNode> {
    tick(iterations?: number): Simulation<N>;
    stop(): Simulation<N>;
    nodes(): N[];
    nodes(nodes: N[]): Simulation<N>;
    alpha(): number;
    alpha(value: number): Simulation<N>;
    alphaTarget(value: number): Simulation<N>;
    alphaDecay(value: number): Simulation<N>;
    velocityDecay(value: number): Simulation<N>;
    force(name: string): Force<N> | undefined;
    force(name: string, force: Force<N> | null): Simulation<N>;
  }

  export interface PositionForce<N extends SimNode> extends Force<N> {
    strength(value: number): PositionForce<N>;
  }

  export interface LinkForce<N extends SimNode, L> extends Force<N> {
    id(fn: (node: N) => string): LinkForce<N, L>;
    distance(value: number | ((link: L) => number)): LinkForce<N, L>;
    strength(value: number | ((link: L) => number)): LinkForce<N, L>;
    links(): L[];
    links(links: L[]): LinkForce<N, L>;
  }

  export interface ManyBodyForce<N extends SimNode> extends Force<N> {
    strength(value: number): ManyBodyForce<N>;
    distanceMax(value: number): ManyBodyForce<N>;
    theta(value: number): ManyBodyForce<N>;
  }

  export function forceSimulation<N extends SimNode>(nodes?: N[], numDimensions?: number): Simulation<N>;
  export function forceX<N extends SimNode>(x?: number): PositionForce<N>;
  export function forceY<N extends SimNode>(y?: number): PositionForce<N>;
  export function forceZ<N extends SimNode>(z?: number): PositionForce<N>;
  export function forceLink<N extends SimNode, L>(links?: L[]): LinkForce<N, L>;
  export function forceManyBody<N extends SimNode>(): ManyBodyForce<N>;
  export function forceCenter<N extends SimNode>(): Force<N>;
}
