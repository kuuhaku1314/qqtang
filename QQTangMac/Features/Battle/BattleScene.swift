import AppKit
import SpriteKit

struct BattleHUDState: Equatable {
    var remainingSeconds = 180
    var score = 0
    var bombsAvailable = 2
    var cratesRemaining = 0
    var enemiesRemaining = 0
    var isFinished = false
    var resultText: String?
}

/// 4.3 竞技规则（无道具场）：
/// 放糖泡 → 十字爆炸/连爆 → 命中后被泡住（可挣扎）→
/// 队友触碰救人、敌人触碰即淘汰 → 一队全灭判负，时间到平局。
final class BattleScene: SKScene {
    var onBombPlaceholder: (() -> Void)?
    var onStateChanged: ((BattleHUDState) -> Void)?
    var playerSpriteCode = "n02"
    var participants: [BattleParticipant] = []

    private var map = BattleMap(
        legacyMapId: 901,
        name: "功夫01",
        width: 15,
        height: 13,
        mapFile: "pig01_4.map",
        previewFile: "pig01_4.img",
        blocked: [],
        spawnPoints: [GridPoint(x: 1, y: 1)]
    )

    private enum Facing: String, CaseIterable {
        case down
        case left
        case right
        case up
    }

    private struct ActiveBomb {
        let point: GridPoint
        let ownerUid: Int64
        let node: SKNode
        let detonateAt: TimeInterval
    }

    private final class BattleActor {
        let info: BattleParticipant
        var point: GridPoint
        var node: SKSpriteNode
        var textures: [Facing: [SKTexture]]
        var facing: Facing = .down
        var walkFrame = 0
        var trappedUntil: TimeInterval?
        var isDead = false
        var nextBotMoveAt: TimeInterval = 0
        var isAggro = false
        var bubble: SKNode?
        var nameLabel: SKLabelNode?

        var isTrapped: Bool { trappedUntil != nil }
        var isActive: Bool { !isDead && !isTrapped }

        init(info: BattleParticipant, point: GridPoint, node: SKSpriteNode, textures: [Facing: [SKTexture]]) {
            self.info = info
            self.point = point
            self.node = node
            self.textures = textures
        }
    }

    private let maxBombsPerPlayer = 2
    private let bombPower = 2
    private let bombFuse: TimeInterval = 3.0
    private let trapDuration: TimeInterval = 8.0
    private let matchSeconds = 180

    private var mapTexture: SKTexture?
    private var actors: [BattleActor] = []
    private var heldKeys = Set<UInt16>()
    private var moveCooldown: TimeInterval = 0
    private let stepInterval: TimeInterval = 0.105
    private var tileSize: CGFloat = 40
    private var boardOrigin = CGPoint(x: 4, y: 10)
    private let assets = LegacyAssetStore.shared
    private var hardBlocked = Set<GridPoint>()
    private var destructible = Set<GridPoint>()
    private var destructibleNodes: [GridPoint: SKNode] = [:]
    private var destructibleOrigins: [GridPoint: GridPoint] = [:]
    private var destructibleFootprints: [GridPoint: Set<GridPoint>] = [:]
    private var bombs: [GridPoint: ActiveBomb] = [:]
    /// 糖浆滞留判定（QQTPVE grid_damage_duration = 500ms）：走进糖浆同样被泡。
    private var activeFlames: [GridPoint: TimeInterval] = [:]
    private let flameDuration: TimeInterval = 0.5
    private var currentTime: TimeInterval = 0
    private var battleStartTime: TimeInterval?
    private var hudState = BattleHUDState()

    private var localActor: BattleActor? {
        actors.first { $0.info.isLocal }
    }

    func configure(map: BattleMap) {
        self.map = map
        mapTexture = loadMapTexture(for: map)
        if view != nil {
            restart()
        }
    }

    func restart() {
        removeAllActions()
        removeAllChildren()
        heldKeys.removeAll()
        bombs.removeAll()
        activeFlames.removeAll()
        destructibleNodes.removeAll()
        destructibleOrigins.removeAll()
        destructibleFootprints.removeAll()
        actors.removeAll()
        battleStartTime = nil
        currentTime = 0
        moveCooldown = 0
        rebuild()
        SoundPlayer.play(.readyGo)
    }

    override func didMove(to view: SKView) {
        backgroundColor = SKColor(red: 0.05, green: 0.18, blue: 0.22, alpha: 1)
        view.ignoresSiblingOrder = false
        view.window?.makeFirstResponder(view)
        restart()
    }

    func handleKey(code: UInt16, isDown: Bool) {
        if isDown {
            let isNewPress = !heldKeys.contains(code)
            heldKeys.insert(code)
            guard let local = localActor, !local.isDead else { return }
            if local.isTrapped {
                // 原版：被泡后连打按键可以加快挣脱
                if isNewPress, let until = local.trappedUntil {
                    local.trappedUntil = until - 0.12
                }
                return
            }
            if code == 49, isNewPress {
                placeBomb()
            }
        } else {
            heldKeys.remove(code)
        }
    }

    override func update(_ currentTime: TimeInterval) {
        let deltaTime: TimeInterval
        if self.currentTime == 0 {
            deltaTime = 1 / 60
            battleStartTime = currentTime
        } else {
            deltaTime = min(currentTime - self.currentTime, 0.1)
        }
        self.currentTime = currentTime

        updateClock(currentTime)
        detonateDueBombs(currentTime)

        guard !hudState.isFinished else { return }

        applyLingeringFlames(currentTime)
        updateTrappedActors(currentTime)
        updateBots(currentTime)

        guard let local = localActor, local.isActive else { return }
        moveCooldown -= deltaTime
        guard moveCooldown <= 0, let delta = heldDirection() else { return }
        move(actor: local, dx: delta.x, dy: delta.y, facing: delta.facing)
        moveCooldown = stepInterval
    }

    override func keyDown(with event: NSEvent) {
        handleKey(code: event.keyCode, isDown: true)
    }

    override func keyUp(with event: NSEvent) {
        handleKey(code: event.keyCode, isDown: false)
    }

    // MARK: - 场景搭建

    private func rebuild() {
        let availableWidth = min(size.width - 200, 600)
        let availableHeight = min(size.height - 20, 520)
        tileSize = min(
            availableWidth / CGFloat(map.width),
            availableHeight / CGFloat(map.height)
        )
        let boardWidth = tileSize * CGFloat(map.width)
        let boardHeight = tileSize * CGFloat(map.height)
        boardOrigin = CGPoint(
            x: max(4, (availableWidth - boardWidth) / 2 + 4),
            y: max(4, (size.height - boardHeight) / 2)
        )
        prepareArena()

        let field = SKShapeNode(
            rectOf: CGSize(width: boardWidth, height: boardHeight),
            cornerRadius: 2
        )
        field.position = CGPoint(
            x: boardOrigin.x + boardWidth / 2,
            y: boardOrigin.y + boardHeight / 2
        )
        field.fillColor = SKColor(red: 0.15, green: 0.22, blue: 0.22, alpha: 1)
        field.strokeColor = .clear
        field.zPosition = -10
        addChild(field)

        if !map.elements.isEmpty {
            addParsedMapElements()
        } else if let mapTexture {
            let background = SKSpriteNode(texture: mapTexture)
            background.position = CGPoint(
                x: boardOrigin.x + boardWidth / 2,
                y: boardOrigin.y + boardHeight / 2
            )
            background.size = CGSize(width: boardWidth, height: boardHeight)
            background.zPosition = 0
            background.alpha = 0.72
            addChild(background)
        }

        if map.elements.isEmpty {
            for y in 0..<map.height {
                for x in 0..<map.width {
                    let point = GridPoint(x: x, y: y)
                    addGridTile(at: point)
                    if hardBlocked.contains(point) {
                        addStone(at: point)
                    } else if destructible.contains(point) {
                        addCrate(at: point)
                    }
                }
            }
        }

        let overlay = SKShapeNode(
            rectOf: CGSize(width: boardWidth + 4, height: boardHeight + 4),
            cornerRadius: 4
        )
        overlay.position = CGPoint(
            x: boardOrigin.x + boardWidth / 2,
            y: boardOrigin.y + boardHeight / 2
        )
        overlay.strokeColor = SKColor.white.withAlphaComponent(0.35)
        overlay.lineWidth = 2
        overlay.fillColor = .clear
        overlay.zPosition = 2_000
        addChild(overlay)

        spawnActors()

        hudState = BattleHUDState(
            remainingSeconds: matchSeconds,
            score: 0,
            bombsAvailable: maxBombsPerPlayer,
            cratesRemaining: destructibleFootprints.count,
            enemiesRemaining: enemiesAlive(),
            isFinished: false,
            resultText: nil
        )
        publishHUD()
    }

    private func spawnActors() {
        var roster = participants
        if roster.isEmpty {
            roster = [
                BattleParticipant(
                    uid: 0, name: "玩家", team: .red,
                    spriteCode: playerSpriteCode, isLocal: true
                ),
            ]
        }
        let spawns = map.spawnPoints.isEmpty ? [GridPoint(x: 1, y: 1)] : map.spawnPoints

        for (index, info) in roster.enumerated() {
            let spawn = spawns[index % spawns.count]
            let point = GridPoint(
                x: min(max(spawn.x, 0), map.width - 1),
                y: min(max(spawn.y, 0), map.height - 1)
            )
            let textures = loadTextures(for: info.spriteCode)
            let initialTexture = textures[.down]?.first
            let node = SKSpriteNode(
                texture: initialTexture,
                color: initialTexture == nil
                    ? SKColor(red: 1.0, green: 0.42, blue: 0.18, alpha: 1)
                    : .clear,
                size: CGSize(width: tileSize * 0.86, height: tileSize * 1.18)
            )
            node.colorBlendFactor = initialTexture == nil ? 1 : 0
            addChild(node)

            let label = SKLabelNode(text: info.name)
            label.fontName = "PingFangSC-Semibold"
            label.fontSize = 10
            label.fontColor = info.team == .red
                ? SKColor(red: 1.0, green: 0.62, blue: 0.60, alpha: 1)
                : SKColor(red: 0.56, green: 0.82, blue: 1.0, alpha: 1)
            label.verticalAlignmentMode = .bottom
            label.position = CGPoint(x: 0, y: tileSize * 0.64)
            node.addChild(label)

            let actor = BattleActor(info: info, point: point, node: node, textures: textures)
            actor.nameLabel = label
            actors.append(actor)
            layout(actor)
        }
    }

    private func loadTextures(for spriteCode: String) -> [Facing: [SKTexture]] {
        var result: [Facing: [SKTexture]] = [:]
        for direction in Facing.allCases {
            var frames = (1...6).compactMap { frame in
                Legacy43Assets.image(
                    "Game/Player/\(spriteCode)-c1-\(direction.rawValue)-\(frame).png"
                ).map(SKTexture.init(image:))
            }
            if frames.isEmpty, spriteCode != "n02" {
                frames = (1...6).compactMap { frame in
                    Legacy43Assets.image(
                        "Game/Player/n02-c1-\(direction.rawValue)-\(frame).png"
                    ).map(SKTexture.init(image:))
                }
            }
            result[direction] = frames
        }
        return result
    }

    private func loadMapTexture(for map: BattleMap) -> SKTexture? {
        guard let entry = MapCatalog.entry(id: map.legacyMapId) else {
            return nil
        }
        let image = assets.loadMapImage(for: entry)
            ?? Legacy43Assets.image(
                "Map/\(entry.previewFile.replacingOccurrences(of: ".img", with: "")).png"
            )
        guard let image else { return nil }
        return SKTexture(image: image)
    }

    private func addParsedMapElements() {
        let sorted = map.elements.sorted { lhs, rhs in
            let lhsLayer = renderOrder(for: lhs.layer)
            let rhsLayer = renderOrder(for: rhs.layer)
            if lhsLayer != rhsLayer { return lhsLayer < rhsLayer }
            if lhs.origin.y != rhs.origin.y { return lhs.origin.y < rhs.origin.y }
            return lhs.origin.x > rhs.origin.x
        }
        for element in sorted {
            addMapElement(element)
        }
    }

    private func addMapElement(_ element: BattleMapElement) {
        guard let image = assets.loadMapElementImage(id: element.id) else {
            addMissingMapElement(element)
            return
        }

        let texture = SKTexture(image: image)
        texture.filteringMode = .nearest
        let scale = tileSize / 40
        let imageSize = CGSize(
            width: image.size.width * scale,
            height: image.size.height * scale
        )
        let node = SKSpriteNode(texture: texture, size: imageSize)
        node.anchorPoint = CGPoint(x: 0, y: 0)
        node.position = CGPoint(
            x: boardOrigin.x
                + CGFloat(element.origin.x) * tileSize
                - CGFloat(element.xOffset) * scale,
            y: boardOrigin.y
                + CGFloat(map.height - element.origin.y) * tileSize
                + CGFloat(element.yOffset) * scale
                - imageSize.height
        )
        node.zPosition = depth(for: element)
        node.name = "map-element-\(element.id)"
        addChild(node)

        if element.isDestructible {
            destructibleNodes[element.origin] = node
        }
    }

    private func addMissingMapElement(_ element: BattleMapElement) {
        let size = CGSize(
            width: CGFloat(element.width) * tileSize * 0.92,
            height: CGFloat(element.height) * tileSize * 0.92
        )
        let node = SKShapeNode(rectOf: size, cornerRadius: tileSize * 0.08)
        node.position = CGPoint(
            x: boardOrigin.x
                + (CGFloat(element.origin.x) + CGFloat(element.width) / 2) * tileSize,
            y: boardOrigin.y
                + (CGFloat(map.height - element.origin.y)
                    - CGFloat(element.height) / 2) * tileSize
        )
        node.fillColor = element.layer == 2
            ? SKColor(red: 0.67, green: 0.55, blue: 0.39, alpha: 1)
            : SKColor(red: 0.48, green: 0.29, blue: 0.16, alpha: 1)
        node.strokeColor = .clear
        node.zPosition = depth(for: element)
        addChild(node)
        if element.isDestructible {
            destructibleNodes[element.origin] = node
        }
    }

    private func renderOrder(for layer: Int) -> Int {
        switch layer {
        case 2: 0
        case 1: 1
        default: 2
        }
    }

    private func depth(for element: BattleMapElement) -> CGFloat {
        let bottomRow = element.origin.y + element.height - 1
        let horizontalTieBreak = CGFloat(map.width - element.origin.x) * 0.01
        switch element.layer {
        case 2:
            return horizontalTieBreak
        case 1:
            return 100 + CGFloat(bottomRow) * 10 + horizontalTieBreak
        default:
            return 1_000 + CGFloat(bottomRow) * 10 + horizontalTieBreak
        }
    }

    private func prepareArena() {
        hardBlocked = map.blocked
        destructible = map.destructible
        destructibleOrigins.removeAll()
        destructibleFootprints.removeAll()

        if !map.elements.isEmpty {
            for element in map.elements where element.isDestructible {
                var footprint = Set(element.occupiedPoints.filter { destructible.contains($0) })
                if footprint.isEmpty {
                    footprint.insert(element.origin)
                    destructible.insert(element.origin)
                }
                destructibleFootprints[element.origin] = footprint
                for point in footprint {
                    destructibleOrigins[point] = element.origin
                }
            }
            return
        }

        for x in 0..<map.width {
            hardBlocked.insert(GridPoint(x: x, y: 0))
            hardBlocked.insert(GridPoint(x: x, y: map.height - 1))
        }
        for y in 0..<map.height {
            hardBlocked.insert(GridPoint(x: 0, y: y))
            hardBlocked.insert(GridPoint(x: map.width - 1, y: y))
        }
        for y in stride(from: 2, to: map.height - 1, by: 2) {
            for x in stride(from: 2, to: map.width - 1, by: 2) {
                hardBlocked.insert(GridPoint(x: x, y: y))
            }
        }

        let spawnSafety = Set(map.spawnPoints.flatMap { spawn in
            [
                spawn,
                GridPoint(x: spawn.x + 1, y: spawn.y),
                GridPoint(x: spawn.x - 1, y: spawn.y),
                GridPoint(x: spawn.x, y: spawn.y + 1),
                GridPoint(x: spawn.x, y: spawn.y - 1),
            ]
        })
        destructible.removeAll()
        for y in 1..<(map.height - 1) {
            for x in 1..<(map.width - 1) {
                let point = GridPoint(x: x, y: y)
                guard !hardBlocked.contains(point), !spawnSafety.contains(point) else { continue }
                let seed = x * 17 + y * 31 + map.legacyMapId
                if seed.isMultiple(of: 4) || seed.isMultiple(of: 7) {
                    destructible.insert(point)
                }
            }
        }
        for point in destructible {
            destructibleOrigins[point] = point
            destructibleFootprints[point] = [point]
        }
    }

    private func addGridTile(at point: GridPoint) {
        let tile = SKShapeNode(
            rectOf: CGSize(width: tileSize - 1, height: tileSize - 1),
            cornerRadius: 2
        )
        tile.position = gridPosition(for: point)
        tile.fillColor = SKColor.white.withAlphaComponent(
            (point.x + point.y).isMultiple(of: 2) ? 0.055 : 0.025
        )
        tile.strokeColor = SKColor.black.withAlphaComponent(0.08)
        tile.lineWidth = 0.5
        tile.zPosition = 1
        addChild(tile)
    }

    private func addStone(at point: GridPoint) {
        let stone = SKShapeNode(
            rectOf: CGSize(width: tileSize * 0.86, height: tileSize * 0.86),
            cornerRadius: tileSize * 0.17
        )
        stone.position = gridPosition(for: point)
        stone.fillColor = SKColor(red: 0.20, green: 0.55, blue: 0.42, alpha: 0.94)
        stone.strokeColor = SKColor(red: 0.74, green: 0.95, blue: 0.66, alpha: 0.9)
        stone.lineWidth = 2
        stone.zPosition = 8
        addChild(stone)
    }

    private func addCrate(at point: GridPoint) {
        let crate = SKShapeNode(
            rectOf: CGSize(width: tileSize * 0.82, height: tileSize * 0.82),
            cornerRadius: tileSize * 0.12
        )
        crate.position = gridPosition(for: point)
        crate.fillColor = SKColor(red: 0.78, green: 0.42, blue: 0.16, alpha: 0.96)
        crate.strokeColor = SKColor(red: 1.0, green: 0.78, blue: 0.30, alpha: 1)
        crate.lineWidth = 2
        crate.zPosition = 10

        let mark = SKLabelNode(text: "糖")
        mark.fontName = "PingFangSC-Semibold"
        mark.fontSize = tileSize * 0.39
        mark.fontColor = SKColor.white.withAlphaComponent(0.82)
        mark.verticalAlignmentMode = .center
        mark.position.y = -tileSize * 0.04
        crate.addChild(mark)

        destructibleNodes[point] = crate
        addChild(crate)
    }

    private func gridPosition(for point: GridPoint) -> CGPoint {
        CGPoint(
            x: boardOrigin.x + CGFloat(point.x) * tileSize + tileSize / 2,
            y: boardOrigin.y
                + CGFloat(map.height - point.y) * tileSize
                - tileSize / 2
        )
    }

    // MARK: - 移动

    private func layout(_ actor: BattleActor) {
        var position = gridPosition(for: actor.point)
        position.y += tileSize * 0.10
        actor.node.position = position
        actor.node.zPosition = actorDepth(at: actor.point)
    }

    private func move(actor: BattleActor, dx: Int, dy: Int, facing: Facing) {
        let next = GridPoint(x: actor.point.x + dx, y: actor.point.y + dy)
        guard isWalkable(next) else { return }
        actor.facing = facing
        actor.point = next
        actor.walkFrame = (actor.walkFrame + 1) % 6
        if let texture = actor.textures[facing], !texture.isEmpty {
            actor.node.texture = texture[actor.walkFrame % texture.count]
            actor.node.colorBlendFactor = 0
        }
        actor.node.zPosition = actorDepth(at: next)
        var position = gridPosition(for: next)
        position.y += tileSize * 0.10
        actor.node.run(.move(to: position, duration: stepInterval * 0.76))
        resolveTouches(around: actor)
    }

    private func isWalkable(_ point: GridPoint) -> Bool {
        guard point.x >= 0, point.y >= 0, point.x < map.width, point.y < map.height else { return false }
        return !hardBlocked.contains(point)
            && !destructible.contains(point)
            && bombs[point] == nil
    }

    private func heldDirection() -> (x: Int, y: Int, facing: Facing)? {
        if heldKeys.contains(126) || heldKeys.contains(13) { return (0, -1, .up) }
        if heldKeys.contains(125) || heldKeys.contains(1) { return (0, 1, .down) }
        if heldKeys.contains(123) || heldKeys.contains(0) { return (-1, 0, .left) }
        if heldKeys.contains(124) || heldKeys.contains(2) { return (1, 0, .right) }
        return nil
    }

    private func actorDepth(at point: GridPoint) -> CGFloat {
        105 + CGFloat(point.y) * 10
    }

    // MARK: - 电脑玩家（原版怪物机制：方形视野→永久仇恨→最短路追踪，无路则定身）

    /// 原版 resent_dist：以格为单位的正方形视野。
    private let botVisionRange = 3

    private func updateBots(_ time: TimeInterval) {
        guard let local = localActor, !local.isDead else { return }
        for actor in actors where !actor.info.isLocal && actor.isActive {
            let isEnemy = actor.info.team != local.info.team

            // 进入视野后永久仇恨（原版仇恨不消失）
            if isEnemy, !actor.isAggro,
               abs(actor.point.x - local.point.x) <= botVisionRange,
               abs(actor.point.y - local.point.y) <= botVisionRange
            {
                actor.isAggro = true
            }

            guard time >= actor.nextBotMoveAt else { continue }

            if isEnemy, actor.isAggro {
                actor.nextBotMoveAt = time + 0.32
                // 寻路目标是玩家所在格；玩家与糖泡同格（隐泡）时无连通路径 → 原地定身
                guard let step = nextStepTowards(local.point, from: actor.point) else { continue }
                move(
                    actor: actor,
                    dx: step.x - actor.point.x,
                    dy: step.y - actor.point.y,
                    facing: facingFor(from: actor.point, to: step)
                )
            } else {
                // 未仇恨/队友：区域内随机游走
                actor.nextBotMoveAt = time + Double.random(in: 0.34...0.62)
                let options: [(Int, Int, Facing)] = [
                    (0, 1, .down), (0, -1, .up), (-1, 0, .left), (1, 0, .right),
                ].filter { isWalkable(GridPoint(x: actor.point.x + $0.0, y: actor.point.y + $0.1)) }
                guard let choice = options.randomElement() else { continue }
                move(actor: actor, dx: choice.0, dy: choice.1, facing: choice.2)
            }
        }
    }

    private func facingFor(from: GridPoint, to: GridPoint) -> Facing {
        if to.x > from.x { return .right }
        if to.x < from.x { return .left }
        if to.y > from.y { return .down }
        return .up
    }

    /// BFS 最短路（格数均权，等价原版 A*）；目标格不可达时返回 nil。
    private func nextStepTowards(_ target: GridPoint, from start: GridPoint) -> GridPoint? {
        guard start != target else { return nil }
        var cameFrom: [GridPoint: GridPoint] = [:]
        var visited: Set<GridPoint> = [start]
        var queue = [start]
        var head = 0
        while head < queue.count {
            let point = queue[head]
            head += 1
            for delta in [(1, 0), (-1, 0), (0, 1), (0, -1)] {
                let next = GridPoint(x: point.x + delta.0, y: point.y + delta.1)
                guard !visited.contains(next) else { continue }
                // 糖泡只出不进：玩家与糖泡同格（隐泡）时目标格同样不可达 → 定身
                guard isWalkable(next) else { continue }
                visited.insert(next)
                cameFrom[next] = point
                if next == target {
                    var step = next
                    while let previous = cameFrom[step], previous != start {
                        step = previous
                    }
                    return step
                }
                queue.append(next)
            }
        }
        return nil
    }

    // MARK: - 糖泡

    private func placeBomb() {
        guard !hudState.isFinished,
              let local = localActor, local.isActive,
              bombs.values.filter({ $0.ownerUid == local.info.uid }).count < maxBombsPerPlayer,
              bombs[local.point] == nil
        else { return }

        let container = SKNode()
        container.position = gridPosition(for: local.point)
        container.zPosition = actorDepth(at: local.point) + 1

        let bubble = SKShapeNode(circleOfRadius: tileSize * 0.32)
        bubble.fillColor = SKColor(red: 0.14, green: 0.77, blue: 1.0, alpha: 0.90)
        bubble.strokeColor = .white
        bubble.lineWidth = 2.5
        container.addChild(bubble)

        let highlight = SKShapeNode(circleOfRadius: tileSize * 0.085)
        highlight.fillColor = SKColor.white.withAlphaComponent(0.88)
        highlight.strokeColor = .clear
        highlight.position = CGPoint(x: -tileSize * 0.10, y: tileSize * 0.11)
        container.addChild(highlight)

        bubble.run(.repeatForever(.sequence([
            .scale(to: 1.13, duration: 0.24),
            .scale(to: 0.94, duration: 0.24),
        ])))
        addChild(container)
        bombs[local.point] = ActiveBomb(
            point: local.point,
            ownerUid: local.info.uid,
            node: container,
            detonateAt: currentTime + bombFuse
        )
        SoundPlayer.play(.bomb)
        refreshBombHUD()
        onBombPlaceholder?()
    }

    private func detonateDueBombs(_ time: TimeInterval) {
        let due = bombs.values
            .filter { $0.detonateAt <= time }
            .map(\.point)
        for point in due {
            detonateBomb(at: point)
        }
    }

    private func detonateBomb(at point: GridPoint) {
        guard let bomb = bombs.removeValue(forKey: point) else { return }
        bomb.node.removeAllActions()
        bomb.node.removeFromParent()
        SoundPlayer.play(.explode)

        var flamePoints = [point]
        let directions = [(1, 0), (-1, 0), (0, 1), (0, -1)]
        for direction in directions {
            for distance in 1...bombPower {
                let next = GridPoint(
                    x: point.x + direction.0 * distance,
                    y: point.y + direction.1 * distance
                )
                if hardBlocked.contains(next) { break }
                flamePoints.append(next)
                if destructible.contains(next) {
                    destroyCrate(at: next)
                    break
                }
            }
        }

        for flamePoint in flamePoints {
            addFlame(at: flamePoint)
        }
        for chainedPoint in flamePoints where bombs[chainedPoint] != nil {
            detonateBomb(at: chainedPoint)
        }

        // 原版：命中不是死亡，而是被泡住
        let flameSet = Set(flamePoints)
        for actor in actors where !actor.isDead && !actor.isTrapped {
            if flameSet.contains(actor.point) {
                trap(actor)
            }
        }

        refreshBombHUD()
    }

    private func destroyCrate(at point: GridPoint) {
        guard let origin = destructibleOrigins[point] else { return }
        let footprint = destructibleFootprints.removeValue(forKey: origin) ?? Set([point])
        for occupiedPoint in footprint {
            destructible.remove(occupiedPoint)
            destructibleOrigins.removeValue(forKey: occupiedPoint)
        }
        if let node = destructibleNodes.removeValue(forKey: origin) {
            node.run(.sequence([
                .group([
                    .scale(to: 1.35, duration: 0.12),
                    .fadeOut(withDuration: 0.20),
                    .rotate(byAngle: .pi / 5, duration: 0.20),
                ]),
                .removeFromParent(),
            ]))
        }
        hudState.score += 10
        hudState.cratesRemaining = destructibleFootprints.count
        publishHUD()
    }

    /// 糖浆在格子上滞留 0.5 秒，期间踏入同样被泡。
    private func applyLingeringFlames(_ time: TimeInterval) {
        activeFlames = activeFlames.filter { $0.value > time }
        guard !activeFlames.isEmpty else { return }
        for actor in actors where !actor.isDead && !actor.isTrapped {
            if activeFlames[actor.point] != nil {
                trap(actor)
            }
        }
    }

    private func addFlame(at point: GridPoint) {
        activeFlames[point] = currentTime + flameDuration
        let flame = SKShapeNode(circleOfRadius: tileSize * 0.43)
        flame.position = gridPosition(for: point)
        flame.fillColor = SKColor(red: 0.12, green: 0.88, blue: 1.0, alpha: 0.78)
        flame.strokeColor = SKColor.white.withAlphaComponent(0.94)
        flame.lineWidth = 3
        flame.zPosition = 1_500

        let core = SKShapeNode(circleOfRadius: tileSize * 0.17)
        core.fillColor = SKColor.white.withAlphaComponent(0.92)
        core.strokeColor = .clear
        flame.addChild(core)
        addChild(flame)
        flame.run(.sequence([
            .group([
                .scale(to: 1.22, duration: 0.16),
                .fadeOut(withDuration: 0.48),
            ]),
            .removeFromParent(),
        ]))
    }

    // MARK: - 被泡 / 救人 / 淘汰

    private func trap(_ actor: BattleActor) {
        actor.trappedUntil = currentTime + trapDuration
        SoundPlayer.play(.trap)

        let bubble = SKShapeNode(circleOfRadius: tileSize * 0.56)
        bubble.fillColor = SKColor(red: 0.45, green: 0.86, blue: 1.0, alpha: 0.42)
        bubble.strokeColor = SKColor.white.withAlphaComponent(0.9)
        bubble.lineWidth = 2.5
        bubble.zPosition = 4
        bubble.run(.repeatForever(.sequence([
            .scale(to: 1.09, duration: 0.36),
            .scale(to: 0.95, duration: 0.36),
        ])))
        actor.node.addChild(bubble)
        actor.bubble = bubble
    }

    private func rescue(_ actor: BattleActor, by rescuer: BattleActor) {
        actor.trappedUntil = nil
        actor.bubble?.removeFromParent()
        actor.bubble = nil
        SoundPlayer.play(.item)
        if rescuer.info.isLocal {
            hudState.score += 200
            publishHUD()
        }
    }

    private func kill(_ actor: BattleActor) {
        actor.trappedUntil = nil
        actor.bubble?.removeFromParent()
        actor.bubble = nil
        actor.isDead = true
        SoundPlayer.play(.heroDead)
        actor.node.run(.sequence([
            .group([
                .scale(to: 1.3, duration: 0.14),
                .fadeAlpha(to: 0.0, duration: 0.30),
            ]),
        ]))
        actor.nameLabel?.alpha = 0.4
        checkForResult()
    }

    private func updateTrappedActors(_ time: TimeInterval) {
        for actor in actors where !actor.isDead {
            if let until = actor.trappedUntil, time >= until {
                kill(actor)
            }
        }
    }

    /// 触碰判定：走到被泡玩家旁（含同格）——队友解救，敌人淘汰。
    private func resolveTouches(around actor: BattleActor) {
        guard actor.isActive else { return }
        for other in actors where other !== actor && other.isTrapped && !other.isDead {
            let distance = abs(other.point.x - actor.point.x) + abs(other.point.y - actor.point.y)
            guard distance <= 1 else { continue }
            if other.info.team == actor.info.team {
                rescue(other, by: actor)
            } else {
                if actor.info.isLocal {
                    hudState.score += 500
                }
                kill(other)
            }
        }
    }

    private func enemiesAlive() -> Int {
        guard let local = localActor else { return 0 }
        return actors.filter { $0.info.team != local.info.team && !$0.isDead }.count
    }

    private func checkForResult() {
        guard !hudState.isFinished else { return }
        hudState.enemiesRemaining = enemiesAlive()

        if let local = localActor, local.isDead {
            finish(with: "失败…")
            return
        }
        if let local = localActor {
            let enemies = actors.filter { $0.info.team != local.info.team }
            if !enemies.isEmpty, enemies.allSatisfy(\.isDead) {
                finish(with: "胜利！")
                return
            }
        }
        publishHUD()
    }

    // MARK: - 计时 / 结果

    private func updateClock(_ time: TimeInterval) {
        guard !hudState.isFinished, let battleStartTime else { return }
        let remaining = max(0, matchSeconds - Int(time - battleStartTime))
        if remaining != hudState.remainingSeconds {
            hudState.remainingSeconds = remaining
            publishHUD()
        }
        if remaining == 0 {
            finish(with: "时间到，平局")
        }
    }

    private func finish(with text: String) {
        guard !hudState.isFinished else { return }
        hudState.isFinished = true
        hudState.resultText = text
        hudState.enemiesRemaining = enemiesAlive()
        heldKeys.removeAll()
        localActor?.node.run(.repeat(.sequence([
            .fadeAlpha(to: 0.25, duration: 0.10),
            .fadeAlpha(to: 1.0, duration: 0.10),
        ]), count: 4))
        publishHUD()
    }

    private func refreshBombHUD() {
        guard let local = localActor else { return }
        let mine = bombs.values.filter { $0.ownerUid == local.info.uid }.count
        hudState.bombsAvailable = max(0, maxBombsPerPlayer - mine)
        publishHUD()
    }

    private func publishHUD() {
        onStateChanged?(hudState)
    }
}
