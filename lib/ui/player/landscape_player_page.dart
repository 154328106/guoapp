import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';

import '../../app_device.dart';
import '../../local_store.dart';
import '../../models.dart';
import '../../playback_preferences.dart';
import '../../playback_session.dart';
import '../app_scope.dart';
import '../downloads/download_actions.dart';
import '../theme/app_theme.dart';
import '../theme/motion.dart';
import '../widgets/motion/motion_widgets.dart';
import 'danmaku_overlay.dart';
import '../widgets/status_view.dart';
import '../widgets/follow_button.dart';
import 'episode_grid.dart';
import 'mini_player.dart';
import 'playback_handle.dart';
import 'playback_sheets.dart';
import 'fade_presence.dart';
import 'player_surface.dart';
import 'swipe_controls.dart';
import 'window_fullscreen.dart';

enum _Panel { episodes, settings, speed, quality }

class LandscapePlayerPage extends StatefulWidget {
  const LandscapePlayerPage({
    super.key,
    required this.store,
    required this.handle,
    this.onFullscreen,
    this.onMiniWindow,
  });
  final LocalStore store;
  final PlaybackHandle handle;
  final VoidCallback? onFullscreen;
  final VoidCallback? onMiniWindow;

  @override
  State<LandscapePlayerPage> createState() => _LandscapePlayerPageState();
}

class _LandscapePlayerPageState extends State<LandscapePlayerPage>
    with WidgetsBindingObserver, SingleTickerProviderStateMixin {
  final List<StreamSubscription<dynamic>> _subscriptions = [];
  final _selected = <int>{};
  final _links = {_Panel.speed: LayerLink(), _Panel.quality: LayerLink()};
  Player? _bound;
  Timer? _hideTimer;
  Timer? _tapTimer;
  Timer? _streakTimer;
  Timer? _clockTimer;
  Timer? _seekTimer;
  bool? _full;
  bool _visible = true;
  bool _selecting = false;

  /// 竖屏选集面板：0 收起（视频居中 + 底部选集条），1 展开（视频到顶 + 面板）
  late final AnimationController _sheet;
  double _sheetExtent = 1;
  bool _pulling = false;
  double _swipeDy = 0;
  int _group = 0;
  int? _groupFor;
  bool _holding = false;
  bool _dragging = false;
  double? _seekValue;
  double? _seekTarget;
  int _streak = 0;
  int _streakTaps = 0;
  _Panel? _panel;
  int _sidePanelMount = 0;
  bool _sidePanelShown = false;
  DateTime _now = DateTime.now();
  ({int level, bool charging})? _battery;

  PlaybackSession get _session => widget.handle.session;

  @override
  void initState() {
    super.initState();
    _sheet =
        AnimationController(
          vsync: this,
          duration: const Duration(milliseconds: 320),
        )..addStatusListener((status) {
          if (mounted &&
              (status == AnimationStatus.completed ||
                  status == AnimationStatus.dismissed)) {
            setState(() {});
          }
        });
    WidgetsBinding.instance.addObserver(this);
    if (widget.onFullscreen == null) {
      unawaited(AppDevice.setPlayerOrientation(PlayerOrientation.auto));
    }
    _session.addListener(_bind);
    _bind();
    _scheduleHide();
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final full = MediaQuery.orientationOf(context) == Orientation.landscape;
    if (full == _full) return;
    _full = full;
    _panel = null;
    _visible = true;
    _scheduleHide();
    unawaited(
      SystemChrome.setEnabledSystemUIMode(
        full ? SystemUiMode.immersiveSticky : SystemUiMode.edgeToEdge,
      ).catchError((Object _) {}),
    );
    _clockTimer?.cancel();
    _clockTimer = null;
    if (full) {
      _tick();
      _clockTimer = Timer.periodic(const Duration(seconds: 20), (_) => _tick());
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _sheet.dispose();
    _session.removeListener(_bind);
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    _hideTimer?.cancel();
    _tapTimer?.cancel();
    _streakTimer?.cancel();
    _clockTimer?.cancel();
    _seekTimer?.cancel();
    if (_holding) _session.endBoost();
    if (widget.onFullscreen == null) {
      if (_full == true && desktopPlatform) {
        unawaited(setWindowFullScreen(false));
      }
      unawaited(AppDevice.releaseOrientation());
      unawaited(
        SystemChrome.setEnabledSystemUIMode(
          SystemUiMode.edgeToEdge,
        ).catchError((Object _) {}),
      );
    }
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) =>
      _session.setForeground(playerVisible(state));

  void _bind() {
    final player = _session.player;
    if (identical(player, _bound)) {
      _refresh();
      return;
    }
    _bound = player;
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    _subscriptions.clear();
    final stream = player.stream;
    _subscriptions.add(
      stream.position.listen((position) {
        final target = _seekTarget;
        if (target != null && (position.inMilliseconds - target).abs() < 1000) {
          _endSeek();
        }
      }),
    );
    _subscriptions.add(
      stream.playing.listen((playing) {
        if (playing) _scheduleHide();
        _refresh();
      }),
    );
    for (final changes in [stream.duration, stream.buffering]) {
      _subscriptions.add(changes.listen((_) => _refresh()));
    }
    _refresh();
  }

  void _refresh() {
    if (mounted) setState(() {});
  }

  void _tick() {
    _now = DateTime.now();
    _refresh();
    unawaited(
      AppDevice.battery().then((battery) {
        if (mounted) setState(() => _battery = battery);
      }),
    );
  }

  void _endSeek() {
    _seekTimer?.cancel();
    _seekTarget = null;
    _seekValue = null;
  }

  bool get _pinned =>
      _session.paused || !_session.ready || _dragging || _panel != null;

  void _scheduleHide() {
    _hideTimer?.cancel();
    _hideTimer = Timer(const Duration(seconds: 3), () {
      if (!mounted || _pinned || !_visible) return;
      setState(() => _visible = false);
    });
  }

  void _show() {
    if (!_visible) setState(() => _visible = true);
    _scheduleHide();
  }

  void _toggleControls() {
    if (_visible && !_pinned) {
      _hideTimer?.cancel();
      setState(() => _visible = false);
    } else {
      _show();
    }
  }

  void _tapUp(TapUpDetails details, double width, bool full) {
    final x = details.localPosition.dx;
    final side =
        !full && widget.onFullscreen == null ||
            !_session.store.playbackPreferences.doubleTapSeek
        ? 0
        : x < width / 4
        ? -1
        : x > width * 3 / 4
        ? 1
        : 0;
    if (_streak != 0) {
      if (side == _streak.sign) return _streakTap(side);
      setState(_commitStreak);
    }
    if (_tapTimer?.isActive ?? false) {
      _tapTimer!.cancel();
      if (side == 0) {
        _session.togglePlayback();
      } else {
        _startStreak(side);
      }
      return;
    }
    _tapTimer = Timer(const Duration(milliseconds: 300), _toggleControls);
  }

  void _startStreak(int side) {
    final duration = _session.player.state.duration;
    if (!_session.ready || duration <= Duration.zero) return;
    _streak = side;
    _streakTaps = 0;
    unawaited(HapticFeedback.selectionClick());
    _armStreak();
  }

  void _streakTap(int side) {
    _streakTaps++;
    if (_streakTaps.isEven) {
      _streak += side;
      unawaited(HapticFeedback.selectionClick());
    }
    _armStreak();
  }

  void _armStreak() {
    _streakTimer?.cancel();
    _streakTimer = Timer(const Duration(milliseconds: 700), () {
      if (mounted) setState(_commitStreak);
    });
    setState(() {});
  }

  void _commitStreak() {
    _streakTimer?.cancel();
    final steps = _streak;
    _streak = 0;
    final state = _session.player.state;
    if (steps == 0 || !_session.ready || state.duration <= Duration.zero) {
      return;
    }
    final target = (state.position.inMilliseconds + steps * 10000).clamp(
      0,
      state.duration.inMilliseconds,
    );
    unawaited(_session.seekTo(Duration(milliseconds: target)));
  }

  void _pressStart() {
    if (!_session.ready) return;
    _session.holdBoost();
    unawaited(HapticFeedback.selectionClick());
    setState(() => _holding = true);
  }

  void _pressEnd() {
    if (!_holding) return;
    _session.endBoost();
    setState(() => _holding = false);
  }

  void _rotate(bool full) => unawaited(
    desktopPlatform
        ? setWindowFullScreen(!full)
        : AppDevice.setPlayerOrientation(
            full ? PlayerOrientation.portrait : PlayerOrientation.landscape,
          ),
  );

  void _back(bool full) {
    if (_panel != null) {
      setState(() => _panel = null);
      _scheduleHide();
    } else if (full) {
      _rotate(true);
    } else if (_selecting) {
      _select(false);
    } else if (_sheet.value > 0) {
      _closeSheet();
    }
  }

  void _openSheet() =>
      unawaited(_sheet.animateTo(1, curve: Curves.easeOutCubic));

  void _closeSheet() =>
      unawaited(_sheet.animateBack(0, curve: Curves.easeOutCubic));

  void _dragSheet(DragUpdateDetails details) =>
      _sheet.value -= (details.primaryDelta ?? 0) / _sheetExtent;

  void _settleSheet([double velocity = 0]) {
    if (velocity > 300) {
      _closeSheet();
    } else if (velocity < -300) {
      _openSheet();
    } else {
      _sheet.value > .6 ? _openSheet() : _closeSheet();
    }
  }

  void _swipeEpisode(DragEndDetails details) {
    final velocity = details.primaryVelocity ?? 0;
    final distance = _swipeDy;
    _swipeDy = 0;
    final up = velocity < -400 || velocity.abs() <= 400 && distance < -80;
    final down = velocity > 400 || velocity.abs() <= 400 && distance > 80;
    if (!up && !down) return;
    final session = _session;
    final target = session.index + (up ? 1 : -1);
    if (target < 0 || target >= session.detail.episodes.length) {
      ScaffoldMessenger.of(context)
        ..hideCurrentSnackBar()
        ..showSnackBar(
          SnackBar(
            content: Text(up ? '已经是最后一集了' : '已经是第一集了'),
            duration: const Duration(seconds: 1),
          ),
        );
      return;
    }
    unawaited(HapticFeedback.selectionClick());
    unawaited(session.play(target));
  }

  /// 列表已经滚到顶还往下拉 → 带着面板一起往下收
  bool _pullToClose(ScrollNotification notification) {
    if (notification.depth != 0) return false;
    if (notification is OverscrollNotification &&
        notification.dragDetails != null &&
        (notification.overscroll < 0 || _pulling)) {
      _pulling = true;
      _sheet.value += notification.overscroll / _sheetExtent;
    } else if (notification is ScrollEndNotification && _pulling) {
      _pulling = false;
      _settleSheet(notification.dragDetails?.velocity.pixelsPerSecond.dy ?? 0);
    }
    return false;
  }

  void _openPanel(_Panel panel) {
    _hideTimer?.cancel();
    setState(() => _panel = panel);
  }

  void _closePanel() {
    setState(() => _panel = null);
    _scheduleHide();
  }

  void _select(bool selecting) => setState(() {
    _selecting = selecting;
    _selected.clear();
  });

  void _saveFailed() {
    if (!mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('播放偏好未能保存，请重试')));
  }

  Future<void> _change(Future<void> Function() change) async {
    try {
      await change();
    } catch (_) {
      _saveFailed();
    }
  }

  Future<void> _openSettings() {
    _hideTimer?.cancel();
    return showPlayerSheet(
      context,
      (_) => PlaybackSettings(session: _session, onFailure: _saveFailed),
      dark: false,
    ).whenComplete(() {
      if (mounted) _scheduleHide();
    });
  }

  bool get _canDownload =>
      AppScope.of(context).repository.supportsDownloads &&
      widget.store.canDownload;

  void _download() {
    final detail = _session.detail;
    final episodes = [
      for (final index in _selected.toList()..sort()) detail.episodes[index],
    ];
    _select(false);
    unawaited(downloadEpisodes(context, widget.store, detail, episodes));
  }

  @override
  Widget build(BuildContext context) {
    final full = MediaQuery.orientationOf(context) == Orientation.landscape;
    final theme = Theme.of(context);
    final dark = playerTheme(context);
    final video = Theme(
      data: dark,
      child: Builder(builder: (context) => _video(context, full)),
    );
    return PopScope(
      canPop:
          !full &&
          !_selecting &&
          _panel == null &&
          _sheet.status == AnimationStatus.dismissed,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) _back(full);
      },
      child: AnnotatedRegion<SystemUiOverlayStyle>(
        value: full
            ? AppTheme.systemBars(Brightness.dark)
            : AppTheme.systemBars(theme.brightness).copyWith(
                statusBarIconBrightness: Brightness.light,
                statusBarBrightness: Brightness.dark,
              ),
        child: full
            ? Scaffold(backgroundColor: Colors.black, body: video)
            : Scaffold(
                backgroundColor: _stage,
                body: _portrait(context, video),
              ),
      ),
    );
  }

  Widget _video(BuildContext context, bool full) {
    final session = _session;
    final visible = _visible || _pinned;
    final sidePanel = switch (_panel) {
      final panel? when full && _sidePanel(panel) => _panelBody(context, panel),
      _ => null,
    };
    if (sidePanel != null && !_sidePanelShown) _sidePanelMount++;
    _sidePanelShown = sidePanel != null;
    return Stack(
      fit: StackFit.expand,
      children: [
        Video(
          key: ValueKey(session.player),
          controller: widget.handle.video(session.player),
          controls: NoVideoControls,
          fill: Colors.black,
          pauseUponEnteringBackgroundMode: false,
        ),
        DanmakuOverlay(
          controller: session.danmaku,
          aspectRatio: session.aspectRatio,
        ),
        SwipeControls(
          session: session,
          enabled: full,
          levels: session.store.playbackPreferences.swipeLevels,
          child: LayoutBuilder(
            builder: (context, constraints) => GestureDetector(
              behavior: HitTestBehavior.opaque,
              onTapUp: (details) => _tapUp(details, constraints.maxWidth, full),
              onLongPressStart: (_) => _pressStart(),
              onLongPressEnd: (_) => _pressEnd(),
              onLongPressCancel: _pressEnd,
            ),
          ),
        ),
        FadePresence(
          duration: Motion.medium,
          child: session.ready && session.stalled
              ? const IgnorePointer(
                  child: Center(child: CircularProgressIndicator()),
                )
              : null,
        ),
        FadePresence(
          duration: Motion.medium,
          child: session.loading ? PlaybackLoading(session: session) : null,
        ),
        FadePresence(
          duration: Motion.medium,
          child: switch (session.error) {
            final error? => PlaybackFailure(session: session, error: error),
            null => null,
          },
        ),
        FadePresence(
          scaleFrom: .9,
          child: _streak != 0 ? _streakBadge(full) : null,
        ),
        IgnorePointer(
          ignoring: !visible,
          child: AnimatedOpacity(
            opacity: visible ? 1 : 0,
            duration: const Duration(milliseconds: 180),
            child: Listener(
              onPointerDown: (_) => _scheduleHide(),
              child: full ? _fullControls(context) : _miniControls(context),
            ),
          ),
        ),
        if (full && !visible)
          Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            height: 2,
            child: IgnorePointer(child: _thinProgress(context)),
          ),
        Positioned(
          left: 0,
          right: 0,
          top: full ? 24 : 12,
          child: FadePresence(
            child: session.boosting
                ? IgnorePointer(child: Center(child: _boostBadge()))
                : null,
          ),
        ),
        if (session.saveWarning case final warning?)
          Positioned(
            left: 12,
            right: 12,
            bottom: full ? 120 : 64,
            child: _saveWarning(warning),
          ),
        Positioned.fill(
          child: FadePresence(
            child: switch (_panel) {
              final panel? when full => GestureDetector(
                onTap: _closePanel,
                child: ColoredBox(
                  color: _sidePanel(panel)
                      ? const Color(0x4D000000)
                      : Colors.transparent,
                ),
              ),
              _ => null,
            },
          ),
        ),
        Positioned(
          top: 0,
          right: 0,
          bottom: 0,
          width: 360,
          child: FadePresence(
            // A fresh key on every show keeps reopen-during-fade from reusing
            // the closing panel's state, matching the old unmount behavior.
            key: ValueKey(_sidePanelMount),
            duration: Motion.long,
            slideFrom: const Offset(1, 0),
            child: sidePanel,
          ),
        ),
        if (_panel case final panel? when full)
          if (!_sidePanel(panel))
            Positioned(
              left: 0,
              top: 0,
              child: CompositedTransformFollower(
                link: _links[panel]!,
                showWhenUnlinked: false,
                targetAnchor: Alignment.topCenter,
                followerAnchor: Alignment.bottomCenter,
                offset: const Offset(0, -6),
                child: _popup(context, panel),
              ),
            ),
      ],
    );
  }

  bool _sidePanel(_Panel panel) =>
      panel == _Panel.episodes || panel == _Panel.settings;

  Widget _miniControls(BuildContext context) {
    final session = _session;
    return Stack(
      fit: StackFit.expand,
      children: [
        const IgnorePointer(
          child: Align(
            alignment: Alignment.topCenter,
            child: _Shade(height: 64, top: true),
          ),
        ),
        const IgnorePointer(
          child: Align(
            alignment: Alignment.bottomCenter,
            child: _Shade(height: 96, top: false),
          ),
        ),
        Positioned(
          left: 4,
          right: 4,
          top: 0,
          height: 48,
          child: Row(
            children: [
              _icon(
                Icons.arrow_back_rounded,
                '返回',
                () => unawaited(Navigator.maybePop(context)),
              ),
              const Spacer(),
              if (widget.onMiniWindow case final onMiniWindow?)
                _icon(Icons.picture_in_picture_alt_rounded, '小窗', onMiniWindow),
              _icon(Icons.settings_rounded, '播放设置', _openSettings),
            ],
          ),
        ),
        Positioned(
          left: 4,
          right: 4,
          bottom: 4,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Transform.translate(
                offset: const Offset(0, 7),
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 8),
                  child: SizedBox(
                    height: 20,
                    child: _progress(context, thumb: 6),
                  ),
                ),
              ),
              SizedBox(
                height: 44,
                child: Row(
                  children: [
                    _playButton(size: 40, iconSize: 26),
                    const SizedBox(width: 4),
                    _clock(13),
                    const Spacer(),
                    if (session.danmakuSupported) _danmakuButton(context),
                    _icon(
                      Icons.fullscreen_rounded,
                      '全屏',
                      widget.onFullscreen ?? () => _rotate(false),
                      size: 40,
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }

  Widget _fullControls(BuildContext context) {
    final session = _session;
    final padding = MediaQuery.viewPaddingOf(context);
    final left = math.max(padding.left, 20.0);
    final right = math.max(padding.right, 20.0);
    const shadow = [Shadow(color: Color(0x66000000), blurRadius: 2)];
    final ready = session.ready;
    return Stack(
      fit: StackFit.expand,
      children: [
        const IgnorePointer(
          child: Align(
            alignment: Alignment.topCenter,
            child: _Shade(height: 110, top: true),
          ),
        ),
        const IgnorePointer(
          child: Align(
            alignment: Alignment.bottomCenter,
            child: _Shade(height: 150, top: false),
          ),
        ),
        Positioned(
          left: left,
          right: right,
          top: 8,
          height: 56,
          child: Row(
            children: [
              _icon(Icons.arrow_back_rounded, '退出全屏', () => _rotate(true)),
              const SizedBox(width: 4),
              Expanded(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      session.detail.drama.title,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: Colors.white,
                        fontSize: 16,
                        fontWeight: FontWeight.w700,
                        shadows: shadow,
                      ),
                    ),
                    Text(
                      '第 ${session.episode.number} 集',
                      style: TextStyle(
                        color: Colors.white.withValues(alpha: .78),
                        fontSize: 12,
                        shadows: shadow,
                      ),
                    ),
                  ],
                ),
              ),
              FollowIcon(
                store: widget.store,
                drama: session.detail.drama,
                color: Colors.white,
                activeColor: Theme.of(context).colorScheme.primaryFixedDim,
                size: 22,
                extent: 44,
              ),
              const SizedBox(width: 8),
              if (_battery case final battery?) ...[
                _batteryMark(battery),
                const SizedBox(width: 12),
              ],
              Text(
                '${_two(_now.hour)}:${_two(_now.minute)}',
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                  fontFeatures: [FontFeature.tabularFigures()],
                ),
              ),
              const SizedBox(width: 4),
              if (session.danmakuSupported) _danmakuButton(context),
              if (widget.onMiniWindow case final onMiniWindow?)
                _icon(Icons.picture_in_picture_alt_rounded, '小窗', onMiniWindow),
              _icon(
                Icons.settings_rounded,
                '播放设置',
                () => _openPanel(_Panel.settings),
              ),
            ],
          ),
        ),
        Positioned(
          left: left + 8,
          right: right + 8,
          bottom: 60,
          height: 28,
          child: _progress(context, thumb: 8),
        ),
        Positioned(
          left: left,
          right: right,
          bottom: 8,
          height: 48,
          child: Row(
            children: [
              _playButton(size: 44, iconSize: 30),
              const SizedBox(width: 4),
              _clock(13),
              const SizedBox(width: 8),
              _icon(
                Icons.skip_previous_rounded,
                '上一集',
                session.hasPrevious
                    ? () => unawaited(session.previous())
                    : null,
                iconSize: 28,
              ),
              _icon(
                Icons.skip_next_rounded,
                '下一集',
                session.hasNext ? () => unawaited(session.next()) : null,
                iconSize: 28,
              ),
              const Spacer(),
              _icon(
                Icons.playlist_play_rounded,
                '选集',
                () => _openPanel(_Panel.episodes),
                iconSize: 28,
              ),
              CompositedTransformTarget(
                link: _links[_Panel.quality]!,
                child: _textButton(
                  session.qualityLabel,
                  '清晰度',
                  ready && !session.local
                      ? () => _openPanel(_Panel.quality)
                      : null,
                ),
              ),
              CompositedTransformTarget(
                link: _links[_Panel.speed]!,
                child: _textButton(
                  '${session.speed}x',
                  '倍速',
                  ready ? () => _openPanel(_Panel.speed) : null,
                  bold: true,
                ),
              ),
              _icon(Icons.fullscreen_exit_rounded, '退出全屏', () => _rotate(true)),
            ],
          ),
        ),
      ],
    );
  }

  Widget _playButton({required double size, required double iconSize}) {
    final playing = !_session.paused;
    return _icon(
      playing ? Icons.pause_rounded : Icons.play_arrow_rounded,
      playing ? '暂停' : '播放',
      _session.ready ? _session.togglePlayback : null,
      size: size,
      iconSize: iconSize,
    );
  }

  Widget _clock(double fontSize) => PlaybackProgressBuilder(
    player: _session.player,
    builder: (context) {
      final state = _session.player.state;
      final position = _seekValue != null
          ? Duration(milliseconds: _seekValue!.round())
          : state.position;
      return Text(
        '${formatClock(position)} / ${formatClock(state.duration)}',
        maxLines: 1,
        style: TextStyle(
          fontSize: fontSize,
          color: Colors.white.withValues(alpha: .86),
          fontFeatures: const [FontFeature.tabularFigures()],
        ),
      );
    },
  );

  Widget _danmakuButton(BuildContext context) {
    final enabled = _session.danmakuEnabled;
    return _icon(
      enabled ? Icons.subtitles_rounded : Icons.subtitles_off_rounded,
      enabled ? '关闭弹幕' : '打开弹幕',
      () => _change(_session.toggleDanmaku),
      color: enabled ? Theme.of(context).colorScheme.primary : null,
      size: 40,
    );
  }

  Widget _icon(
    IconData icon,
    String label,
    VoidCallback? onPressed, {
    double size = 44,
    double iconSize = 24,
    Color? color,
  }) => IconButton(
    onPressed: onPressed,
    icon: ToggleIconSwitcher(child: Icon(icon, key: ValueKey(icon))),
    iconSize: iconSize,
    tooltip: label,
    color: color ?? Colors.white,
    disabledColor: Colors.white.withValues(alpha: .36),
    style: IconButton.styleFrom(
      fixedSize: Size.square(size),
      padding: EdgeInsets.zero,
    ),
  );

  Widget _textButton(
    String text,
    String label,
    VoidCallback? onPressed, {
    bool bold = false,
  }) => Semantics(
    label: label,
    button: true,
    child: TextButton(
      onPressed: onPressed,
      style: TextButton.styleFrom(
        fixedSize: const Size.fromHeight(40),
        padding: const EdgeInsets.symmetric(horizontal: 12),
        foregroundColor: Colors.white,
        disabledForegroundColor: Colors.white.withValues(alpha: .6),
        textStyle: TextStyle(
          fontSize: 14,
          fontWeight: bold ? FontWeight.w700 : FontWeight.w500,
        ),
      ),
      child: Text(text),
    ),
  );

  Widget _progress(BuildContext context, {required double thumb}) =>
      PlaybackProgressBuilder(
        player: _session.player,
        builder: (context) => _progressBar(context, thumb: thumb),
      );

  Widget _progressBar(BuildContext context, {required double thumb}) {
    final session = _session;
    final state = session.player.state;
    final duration = state.duration;
    final seekable = session.ready && duration > Duration.zero;
    final max = duration.inMilliseconds > 0
        ? duration.inMilliseconds.toDouble()
        : 1.0;
    final position = (_seekValue ?? state.position.inMilliseconds.toDouble())
        .clamp(0.0, max);
    final colors = Theme.of(context).colorScheme;
    return SliderTheme(
      data: SliderTheme.of(context).copyWith(
        trackHeight: 3,
        padding: EdgeInsets.zero,
        activeTrackColor: colors.primary,
        thumbColor: colors.primary,
        inactiveTrackColor: Colors.white.withValues(alpha: .25),
        secondaryActiveTrackColor: Colors.white.withValues(alpha: .4),
        thumbShape: RoundSliderThumbShape(enabledThumbRadius: thumb),
        overlayShape: SliderComponentShape.noOverlay,
        trackShape: const RoundedRectSliderTrackShape(),
      ),
      child: Slider(
        value: position,
        max: max,
        secondaryTrackValue: state.buffer.inMilliseconds.toDouble().clamp(
          0.0,
          max,
        ),
        semanticFormatterCallback: (value) =>
            formatClock(Duration(milliseconds: value.round())),
        onChangeStart: seekable
            ? (value) {
                _seekTimer?.cancel();
                _seekTarget = null;
                _hideTimer?.cancel();
                setState(() {
                  _dragging = true;
                  _seekValue = value;
                });
              }
            : null,
        onChanged: seekable
            ? (value) => setState(() => _seekValue = value)
            : null,
        onChangeEnd: (value) {
          _dragging = false;
          if (seekable) {
            _seekTarget = value;
            _seekTimer?.cancel();
            _seekTimer = Timer(const Duration(seconds: 2), () {
              if (mounted) setState(_endSeek);
            });
            unawaited(session.seekTo(Duration(milliseconds: value.round())));
            setState(() {});
          } else {
            setState(_endSeek);
          }
          _scheduleHide();
        },
      ),
    );
  }

  Widget _thinProgress(BuildContext context) => PlaybackProgressBuilder(
    player: _session.player,
    builder: (context) {
      final state = _session.player.state;
      final duration = state.duration.inMilliseconds;
      return LinearProgressIndicator(
        value: duration > 0 ? state.position.inMilliseconds / duration : 0,
        minHeight: 2,
        color: Theme.of(context).colorScheme.primary,
        backgroundColor: Colors.white.withValues(alpha: .2),
      );
    },
  );

  Widget _streakBadge(bool full) {
    final forward = _streak > 0;
    return IgnorePointer(
      child: Align(
        alignment: Alignment(forward ? .75 : -.75, 0),
        child: Container(
          height: 40,
          padding: const EdgeInsets.symmetric(horizontal: 14),
          decoration: BoxDecoration(
            color: Colors.black.withValues(alpha: .5),
            borderRadius: BorderRadius.circular(20),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            spacing: 6,
            children: [
              Icon(
                forward
                    ? Icons.fast_forward_rounded
                    : Icons.fast_rewind_rounded,
                size: 20,
                color: Colors.white,
              ),
              Text(
                '${forward ? '快进' : '快退'} ${_streak.abs() * 10} 秒',
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _boostBadge() => Container(
    height: 32,
    padding: const EdgeInsets.symmetric(horizontal: 14),
    decoration: BoxDecoration(
      color: Colors.black.withValues(alpha: .5),
      borderRadius: BorderRadius.circular(16),
    ),
    child: const Row(
      mainAxisSize: MainAxisSize.min,
      spacing: 6,
      children: [
        Icon(Icons.fast_forward_rounded, size: 16, color: Colors.white),
        Text(
          '2 倍速播放中',
          style: TextStyle(
            color: Colors.white,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
      ],
    ),
  );

  Widget _batteryMark(({int level, bool charging}) battery) {
    const levels = [
      Icons.battery_0_bar_rounded,
      Icons.battery_1_bar_rounded,
      Icons.battery_2_bar_rounded,
      Icons.battery_3_bar_rounded,
      Icons.battery_4_bar_rounded,
      Icons.battery_5_bar_rounded,
      Icons.battery_6_bar_rounded,
      Icons.battery_full_rounded,
    ];
    final level = battery.level.clamp(0, 100);
    return Semantics(
      label: '电量 $level%',
      excludeSemantics: true,
      child: Row(
        mainAxisSize: MainAxisSize.min,
        spacing: 2,
        children: [
          Icon(
            battery.charging
                ? Icons.battery_charging_full_rounded
                : levels[(level * 7 / 100).round()],
            size: 18,
            color: Colors.white,
          ),
          Text(
            '$level%',
            style: const TextStyle(
              color: Colors.white,
              fontSize: 13,
              fontWeight: FontWeight.w600,
              fontFeatures: [FontFeature.tabularFigures()],
            ),
          ),
        ],
      ),
    );
  }

  Widget _saveWarning(String warning) => Material(
    color: const Color(0xE6322424),
    borderRadius: BorderRadius.circular(10),
    child: Padding(
      padding: const EdgeInsets.fromLTRB(14, 4, 4, 4),
      child: Row(
        children: [
          Expanded(
            child: Text(
              warning,
              style: const TextStyle(color: Colors.white, fontSize: 13),
            ),
          ),
          TextButton(
            onPressed: _session.saveProgress,
            child: const Text('重试保存'),
          ),
        ],
      ),
    ),
  );

  Widget _panelBody(BuildContext context, _Panel panel) {
    final colors = Theme.of(context).colorScheme;
    final right = MediaQuery.viewPaddingOf(context).right;
    final session = _session;
    final episodes = panel == _Panel.episodes;
    return Material(
      color: colors.surfaceContainerLow.withValues(alpha: .94),
      child: Padding(
        padding: EdgeInsets.only(right: right, top: 8),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            SheetHeader(
              children: [
                Text(episodes ? '选集' : '播放设置', style: sheetTitle),
                Expanded(
                  child: Text(
                    episodes ? '共 ${session.detail.episodes.length} 集' : '',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 13,
                      color: colors.onSurfaceVariant,
                    ),
                  ),
                ),
                IconButton(
                  onPressed: _closePanel,
                  icon: const Icon(Icons.close_rounded),
                  tooltip: '关闭',
                ),
              ],
            ),
            Expanded(
              child: episodes
                  ? EpisodeGrid(
                      episodes: session.detail.episodes,
                      current: session.index,
                      selecting: false,
                      selected: const {},
                      touch: true,
                      onSelectionChanged: () {},
                      onPick: (index) {
                        _closePanel();
                        if (index != session.index) {
                          unawaited(session.play(index));
                        }
                      },
                    )
                  : const StatusView(
                      title: '「播放设置」正在重建',
                      icon: Icons.settings_rounded,
                    ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _popup(BuildContext context, _Panel panel) {
    final session = _session;
    return Material(
      color: const Color(0xE0141414),
      borderRadius: BorderRadius.circular(12),
      child: Padding(
        padding: const EdgeInsets.all(6),
        child: panel == _Panel.speed
            ? _options<double>(
                values: playbackSpeeds.reversed.toList(),
                selected: session.speed,
                label: (value) => '${value}x',
                onSelected: (value) => _change(() => session.setSpeed(value)),
              )
            : _options<int>(
                values: [...session.qualities, 0],
                selected: session.quality,
                label: (value) => value == 0 ? '自动' : '${value}P',
                onSelected: (value) => _change(() => session.setQuality(value)),
              ),
      ),
    );
  }

  Widget _options<T>({
    required List<T> values,
    required T selected,
    required String Function(T) label,
    required void Function(T) onSelected,
  }) {
    final colors = Theme.of(context).colorScheme;
    return IntrinsicWidth(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (final value in values)
            InkWell(
              borderRadius: BorderRadius.circular(8),
              onTap: () {
                _closePanel();
                onSelected(value);
              },
              child: Container(
                height: 36,
                constraints: const BoxConstraints(minWidth: 72),
                padding: const EdgeInsets.symmetric(horizontal: 14),
                alignment: Alignment.center,
                child: Text(
                  label(value),
                  style: TextStyle(
                    fontSize: 14,
                    fontWeight: value == selected
                        ? FontWeight.w700
                        : FontWeight.w500,
                    color: value == selected ? colors.primary : Colors.white,
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }

  static const _stage = Color(0xFF121212);
  static const _groupSize = 30;

  /// 竖持横屏视频：收起时视频居中、底部一条「选集」；点开（或上滑）后视频移到顶部，
  /// 下面升起选集面板，下拉把手或在列表顶部继续下拉即可收起
  Widget _portrait(BuildContext context, Widget video) {
    final padding = MediaQuery.paddingOf(context);
    return LayoutBuilder(
      builder: (context, constraints) {
        final height = constraints.maxHeight;
        final videoHeight = constraints.maxWidth * 9 / 16;
        const titleHeight = 48.0;
        const barHeight = 52.0;
        final barBottom = padding.bottom + 14;
        final free =
            height -
            padding.top -
            titleHeight -
            videoHeight -
            barHeight -
            barBottom;
        final collapsedTop =
            padding.top + titleHeight + math.max(0.0, free / 2);
        final expandedTop = padding.top;
        final sheetTop = expandedTop + videoHeight;
        _sheetExtent = math.max(1, height - sheetTop);
        return AnimatedBuilder(
          animation: _sheet,
          builder: (context, _) {
            final t = _sheet.value;
            // 收起时（视频居中）上滑下一集、下滑上一集，跟竖屏短剧一致；面板展开时关掉，免得跟列表滚动抢手势
            final collapsed =
                _sheet.status == AnimationStatus.dismissed && t <= .02;
            return GestureDetector(
              behavior: HitTestBehavior.translucent,
              onVerticalDragStart: collapsed ? (_) => _swipeDy = 0 : null,
              onVerticalDragUpdate: collapsed
                  ? (details) => _swipeDy += details.primaryDelta ?? 0
                  : null,
              onVerticalDragEnd: collapsed ? _swipeEpisode : null,
              child: Stack(
                children: [
                  // 收起时视频上下的空白处：长按打开「播放设置」（倍速、画质等），
                  // 不用再去点视频右上角的设置；长按视频本身仍是 2 倍速
                  Positioned.fill(
                    child: IgnorePointer(
                      ignoring: t > .02,
                      child: GestureDetector(
                        behavior: HitTestBehavior.opaque,
                        onLongPress: () {
                          unawaited(HapticFeedback.selectionClick());
                          unawaited(_openSettings());
                        },
                      ),
                    ),
                  ),
                  Positioned(
                    left: 16,
                    right: 16,
                    top: padding.top,
                    height: titleHeight,
                    child: IgnorePointer(
                      child: Opacity(
                        opacity: (1 - t * 2).clamp(0.0, 1.0),
                        child: _titleBar(),
                      ),
                    ),
                  ),
                  Positioned(
                    left: 0,
                    right: 0,
                    top: collapsedTop + (expandedTop - collapsedTop) * t,
                    height: videoHeight,
                    child: video,
                  ),
                  Positioned(
                    left: 14,
                    right: 14,
                    bottom: barBottom,
                    height: barHeight,
                    child: IgnorePointer(
                      ignoring: t > .02,
                      child: Opacity(
                        opacity: (1 - t * 2).clamp(0.0, 1.0),
                        child: _collapsedBar(),
                      ),
                    ),
                  ),
                  Positioned(
                    left: 0,
                    right: 0,
                    top: sheetTop + (1 - t) * _sheetExtent,
                    height: _sheetExtent,
                    child: _sheetPanel(context),
                  ),
                ],
              ),
            );
          },
        );
      },
    );
  }

  Widget _titleBar() {
    final session = _session;
    return Row(
      children: [
        Flexible(
          child: Text(
            session.detail.drama.title,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(
              color: Colors.white,
              fontSize: 17,
              fontWeight: FontWeight.w700,
            ),
          ),
        ),
        const SizedBox(width: 8),
        Text(
          '第 ${session.index + 1} 集',
          style: const TextStyle(color: Colors.white60, fontSize: 14),
        ),
      ],
    );
  }

  Widget _collapsedBar() {
    final session = _session;
    final drama = session.detail.drama;
    final count = session.detail.episodes.length;
    final status = drama.releaseLabel == '状态未知'
        ? ''
        : ' · ${drama.releaseLabel}';
    return GestureDetector(
      onVerticalDragUpdate: _dragSheet,
      onVerticalDragEnd: (details) =>
          _settleSheet(details.primaryVelocity ?? 0),
      child: Material(
        color: Colors.white.withValues(alpha: .1),
        borderRadius: BorderRadius.circular(12),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: _openSheet,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Row(
              children: [
                const Icon(
                  Icons.video_library_rounded,
                  size: 18,
                  color: Colors.white70,
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: Text(
                    '选集 · 第 ${session.index + 1} / $count 集$status',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      color: Colors.white,
                      fontSize: 15,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                const Icon(
                  Icons.keyboard_arrow_up_rounded,
                  color: Colors.white70,
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _sheetPanel(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return Material(
      color: colors.surface,
      borderRadius: const BorderRadius.vertical(top: Radius.circular(18)),
      clipBehavior: Clip.antiAlias,
      child: Column(
        children: [
          GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTap: _closeSheet,
            onVerticalDragUpdate: _dragSheet,
            onVerticalDragEnd: (details) =>
                _settleSheet(details.primaryVelocity ?? 0),
            child: SizedBox(
              height: 24,
              width: double.infinity,
              child: Center(
                child: Container(
                  width: 36,
                  height: 4,
                  decoration: BoxDecoration(
                    color: colors.outlineVariant,
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
              ),
            ),
          ),
          Expanded(
            child: NotificationListener<ScrollNotification>(
              onNotification: _pullToClose,
              child: _details(context),
            ),
          ),
        ],
      ),
    );
  }

  /// 面板内容：剧集信息 + 选集（每 30 集一页）
  Widget _details(BuildContext context) {
    final session = _session;
    final episodes = session.detail.episodes;
    final all = _selected.length == episodes.length;
    if (_groupFor != session.index) {
      _groupFor = session.index;
      _group = session.index ~/ _groupSize;
    }
    final groups = (episodes.length / _groupSize).ceil();
    final group = groups == 0 ? 0 : _group.clamp(0, groups - 1);
    final start = group * _groupSize;
    final end = math.min(start + _groupSize, episodes.length);
    return SafeArea(
      top: false,
      child: Column(
        children: [
          Expanded(
            child: EpisodeGrid(
              key: ValueKey(start),
              header: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  _info(context),
                  _episodesHeader(context),
                  if (groups > 1) _groupTabs(context, groups, group),
                ],
              ),
              episodes: episodes.sublist(start, end),
              start: start,
              current: session.index,
              selecting: _selecting,
              selected: _selected,
              touch: true,
              minCellWidth: 48,
              onSelectionChanged: () => setState(() {}),
              onPick: (index) {
                if (index != session.index) unawaited(session.play(index));
              },
            ),
          ),
          if (_selecting)
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
              child: Row(
                spacing: 8,
                children: [
                  TextButton(
                    onPressed: () => _select(false),
                    child: const Text('取消'),
                  ),
                  TextButton(
                    onPressed: () => setState(() {
                      all
                          ? _selected.clear()
                          : _selected.addAll(
                              List.generate(episodes.length, (i) => i),
                            );
                    }),
                    child: Text(all ? '取消全选' : '全选'),
                  ),
                  const Spacer(),
                  FilledButton.icon(
                    onPressed: _selected.isEmpty ? null : _download,
                    icon: const Icon(Icons.download_rounded, size: 18),
                    label: const Text('下载'),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }

  Widget _groupTabs(BuildContext context, int groups, int group) {
    final colors = Theme.of(context).colorScheme;
    final count = _session.detail.episodes.length;
    return SizedBox(
      height: 40,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 6),
        itemCount: groups,
        separatorBuilder: (_, _) => const SizedBox(width: 2),
        itemBuilder: (context, index) {
          final selected = index == group;
          final first = index * _groupSize + 1;
          final last = math.min((index + 1) * _groupSize, count);
          return TextButton(
            onPressed: selected ? null : () => setState(() => _group = index),
            style: TextButton.styleFrom(
              foregroundColor: colors.onSurfaceVariant,
              disabledForegroundColor: colors.onSurface,
              minimumSize: const Size(0, 36),
              padding: const EdgeInsets.symmetric(horizontal: 10),
            ),
            child: Text(
              '$first–$last',
              style: TextStyle(
                fontSize: 15,
                fontWeight: selected ? FontWeight.w800 : FontWeight.w500,
                fontFeatures: const [FontFeature.tabularFigures()],
              ),
            ),
          );
        },
      ),
    );
  }

  List<String> _tags() {
    final session = _session;
    final drama = session.detail.drama;
    return [
      SourceSite.byId(drama.source).name,
      if (drama.releaseLabel != '状态未知') drama.releaseLabel,
      '共 ${session.detail.episodes.length} 集',
      if (drama.category.isNotEmpty) drama.category,
    ];
  }

  /// 标题 + 追剧 + 标签 + 两行简介（点开看全文）
  Widget _info(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final drama = _session.detail.drama;
    final description = drama.description.trim();
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 14, 8, 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(
                child: Padding(
                  padding: const EdgeInsets.only(top: 6),
                  child: Text(
                    drama.title,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      fontSize: 19,
                      height: 1.3,
                      fontWeight: FontWeight.w800,
                    ),
                  ),
                ),
              ),
              FollowIcon(
                store: widget.store,
                drama: drama,
                color: colors.onSurfaceVariant,
                activeColor: colors.primary,
                extent: 44,
              ),
            ],
          ),
          const SizedBox(height: 8),
          Padding(
            padding: const EdgeInsets.only(right: 8),
            child: Wrap(
              spacing: 6,
              runSpacing: 6,
              children: [
                for (final (index, tag) in _tags().indexed)
                  _Tag(text: tag, accent: index == 0),
              ],
            ),
          ),
          if (description.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 10, right: 8),
              child: Material(
                type: MaterialType.transparency,
                child: InkWell(
                  borderRadius: BorderRadius.circular(8),
                  onTap: () => _showIntro(context),
                  child: Padding(
                    padding: const EdgeInsets.symmetric(vertical: 4),
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.end,
                      children: [
                        Expanded(
                          child: Text(
                            description,
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              fontSize: 13,
                              height: 1.55,
                              color: colors.onSurfaceVariant,
                            ),
                          ),
                        ),
                        Icon(
                          Icons.chevron_right_rounded,
                          size: 20,
                          color: colors.onSurfaceVariant,
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }

  void _showIntro(BuildContext context) {
    final drama = _session.detail.drama;
    unawaited(
      showModalBottomSheet<void>(
        context: context,
        useRootNavigator: true,
        isScrollControlled: true,
        showDragHandle: true,
        constraints: BoxConstraints(
          maxHeight: MediaQuery.sizeOf(context).height * 0.7,
        ),
        builder: (context) {
          final colors = Theme.of(context).colorScheme;
          return ListView(
            shrinkWrap: true,
            padding: EdgeInsets.fromLTRB(
              20,
              0,
              20,
              24 + MediaQuery.paddingOf(context).bottom,
            ),
            children: [
              Text(
                drama.title,
                style: const TextStyle(
                  fontSize: 19,
                  fontWeight: FontWeight.w800,
                ),
              ),
              const SizedBox(height: 10),
              Wrap(
                spacing: 6,
                runSpacing: 6,
                children: [
                  for (final (index, tag) in _tags().indexed)
                    _Tag(text: tag, accent: index == 0),
                ],
              ),
              const SizedBox(height: 14),
              Text(
                drama.description.trim(),
                style: TextStyle(
                  fontSize: 14,
                  height: 1.7,
                  color: colors.onSurface,
                ),
              ),
            ],
          );
        },
      ),
    );
  }

  Widget _episodesHeader(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final session = _session;
    final episodes = session.detail.episodes;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 10, 4, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Divider(height: 1, endIndent: 12, color: colors.outlineVariant),
          SizedBox(
            height: 48,
            child: Row(
              children: [
                const Text(
                  '选集',
                  style: TextStyle(fontSize: 16, fontWeight: FontWeight.w700),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    _selecting
                        ? '已选 ${_selected.length} 集'
                        : '看到第 ${session.index + 1} 集 / 共 ${episodes.length} 集',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 13,
                      color: colors.onSurfaceVariant,
                    ),
                  ),
                ),
                if (_canDownload && !_selecting)
                  IconButton(
                    onPressed: () => _select(true),
                    icon: const Icon(Icons.download_rounded),
                    tooltip: '下载选集',
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

String _two(int value) => value.toString().padLeft(2, '0');

class _Shade extends StatelessWidget {
  const _Shade({required this.height, required this.top});
  final double height;
  final bool top;

  @override
  Widget build(BuildContext context) => SizedBox(
    width: double.infinity,
    height: height,
    child: DecoratedBox(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: top ? Alignment.topCenter : Alignment.bottomCenter,
          end: top ? Alignment.bottomCenter : Alignment.topCenter,
          colors: const [Color(0x99000000), Color(0x00000000)],
        ),
      ),
    ),
  );
}

class _Tag extends StatelessWidget {
  const _Tag({required this.text, this.accent = false});
  final String text;
  final bool accent;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return DecoratedBox(
      decoration: BoxDecoration(
        color: accent
            ? colors.primaryContainer
            : colors.surfaceContainerHighest.withValues(alpha: 0.7),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
        child: Text(
          text,
          style: TextStyle(
            fontSize: 12,
            height: 1.3,
            fontWeight: accent ? FontWeight.w600 : FontWeight.w500,
            color: accent ? colors.onPrimaryContainer : colors.onSurfaceVariant,
          ),
        ),
      ),
    );
  }
}
