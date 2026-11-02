// Package movement turns a path into the movement packets a real client sends (start, heartbeats, facing changes,
// fall landings, stop), timed by wall clock at the character's run or swim speed.
//
// MovementController follows a path (SetPath) and is stepped with Update. With SpeedFunc and WaterFunc set it
// follows the server's current speed, holds position while that speed is zero (rooted, stunned) and swims where
// the water is over 1.6 yd deep. It sends through MovementSender, which NewWorldMovementSender implements for
// a client.WorldClient, and snaps to ground height with an optional navigation.Navigator. Package session owns one
// controller per character.
package movement
