// Package pathfinding answers spatial queries from AzerothCore's server data: walking paths on the Detour navmesh
// (mmaps/), ground and water heights from the terrain grids (maps/), and heights and line of sight from the
// collision models (vmaps/).
//
// PathFindingService is the entry point (NewPathFindingServiceFromDataDir takes a directory holding mmaps/, maps/
// and vmaps/); it keeps one PathFinder per map and is safe for concurrent use. MMapManager, TerrainManager and
// VMapManager load their files lazily. Package navigation wraps this behind the Navigator interface the rest of
// the module uses.
package pathfinding
